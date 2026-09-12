package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/config"
	"nodevas/internal/db"
	"nodevas/internal/logging"
	"nodevas/internal/mail"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/server"
	"nodevas/web"
)

// isLoopbackHost reports whether a listen host only accepts connections from
// this machine. Everything else is "the network can reach it", which is what
// decides whether accounts are mandatory.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		// An empty host in "":port means every interface.
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// isWildcardHost reports whether the listener answers on every interface, in
// which case the name a browser used to reach it is not knowable from here.
func isWildcardHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	return host == "" || host == "0.0.0.0" || host == "::"
}

func isRemoteDeployment(host string, behindProxy bool) bool {
	return behindProxy || !isLoopbackHost(host)
}

// hostList splits the --hostname value into the names to accept.
func hostList(value string) []string {
	var names []string
	for _, part := range strings.Split(value, ",") {
		if name := strings.ToLower(strings.TrimSpace(part)); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// secretEnvironment reads a secret directly or from the file named by the
// conventional NAME_FILE variable. File-backed secrets keep credentials out
// of container definitions and process listings.
func secretEnvironment(name string) (string, error) {
	value, valueSet := os.LookupEnv(name)
	fileName, fileSet := os.LookupEnv(name + "_FILE")
	fileName = strings.TrimSpace(fileName)
	if valueSet && fileSet && fileName != "" {
		return "", fmt.Errorf("%s and %s_FILE are mutually exclusive", name, name)
	}
	if !fileSet || fileName == "" {
		return value, nil
	}
	data, err := os.ReadFile(fileName)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE %q: %w", name, fileName, err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
}

// validateServeFlags refuses the flag combinations that would put a workspace
// on the network without the protections a networked deployment needs.
//
// It is separate from serve so that the refusals can be exercised without
// binding a port: every one of them is the only thing standing between a
// careless command line and a workspace anyone can reach.
func validateServeFlags(
	listen string,
	port int,
	hostNames []string,
	certFile string,
	keyFile string,
	behindProxy bool,
	allowPlaintext bool,
) error {
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if (certFile == "") != (keyFile == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}
	// A loopback listener behind a reverse proxy is still remotely reachable;
	// treating it as local would silently disable accounts for the common
	// same-host proxy deployment.
	remote := isRemoteDeployment(listen, behindProxy)
	useTLS := certFile != ""
	proxyOnlyTransport := behindProxy && isLoopbackHost(listen)
	if remote && isWildcardHost(listen) && len(hostNames) == 0 {
		return fmt.Errorf("refusing wildcard listen %s without --hostname; configure the public Host name to prevent DNS rebinding", listen)
	}
	if remote && !useTLS && !proxyOnlyTransport && !allowPlaintext {
		return fmt.Errorf(
			"refusing to serve %s without TLS: pass --tls-cert/--tls-key, "+
				"put the server behind an HTTPS reverse proxy, or accept the risk "+
				"with --allow-plaintext", listen)
	}
	return nil
}

// serveArgValue reads one string flag before the full FlagSet is built. The
// project directory determines the default config path, so this small first
// pass lets the config file provide defaults for all of the remaining flags.
func serveArgValue(args []string, name string) (string, bool, error) {
	var value string
	var found bool
	for i, arg := range args {
		for _, prefix := range []string{"-" + name, "--" + name} {
			if arg == prefix {
				if i+1 >= len(args) {
					return "", false, fmt.Errorf("-%s requires a value", name)
				}
				value = args[i+1]
				found = true
				break
			}
			if strings.HasPrefix(arg, prefix+"=") {
				value = strings.TrimPrefix(arg, prefix+"=")
				found = true
				break
			}
		}
	}
	return value, found, nil
}

// bootstrapWorkspace opens everything a workspace is made of: the projects on
// disk, the database beside them, and the search index that spans both.
//
// The database is opened once here and shared with everything that follows:
// SQLite admits one writer, so a second handle would be a second pool queueing
// against this one for it rather than behind it in Go.
//
// Failures end the process rather than travelling back as errors. There is no
// caller above startup that could do anything else with them, and the wording
// each one gets is the whole of what an operator has to go on.
func bootstrapWorkspace(root string) (*realtime.Hub, *project.ProjectManager, *db.DB) {
	hub := realtime.NewHub()
	pm, err := project.NewProjectManager(root, hub)
	if err != nil {
		// Another instance holding the workspace is an ordinary situation on
		// the desktop, where the app spawns this binary and may restart it.
		// Say so plainly instead of dumping it as a generic startup failure.
		var busy *project.WorkspaceBusyError
		if errors.As(err, &busy) {
			fmt.Fprintf(os.Stderr, "%v.\n", busy)
			fmt.Fprintln(os.Stderr,
				"Close the other Nodevas window or stop the running server, then start again.")
			os.Exit(1)
		}
		log.Fatalf("workspace: %v", err)
	}

	// The workspace database holds accounts, the audit trail, settings and the
	// search index.
	database, err := db.Open(root)
	if err != nil {
		log.Fatalf("workspace database: %v", err)
	}
	slog.Info("workspace database ready", slog.String("path", database.Path()))

	// The search index lives in the database now, so it outlives a restart.
	pm.UseDatabase(database)
	// Rows for projects that were deleted while this server was not running
	// have nothing else to notice them; a boot-time sweep is where they go.
	if err := pm.PruneSearchIndex(context.Background()); err != nil {
		slog.Warn("could not prune the search index", logging.Err(err))
	}
	return hub, pm, database
}

func serve(args []string) {
	projectDefault, projectSet, err := serveArgValue(args, "project")
	if err != nil {
		log.Fatal(err)
	}
	if !projectSet || strings.TrimSpace(projectDefault) == "" {
		projectDefault = "."
	}

	configPath, configSet, err := serveArgValue(args, "config")
	if err != nil {
		log.Fatal(err)
	}
	if !configSet {
		if envPath, ok := os.LookupEnv("NODEVAS_CONFIG"); ok && strings.TrimSpace(envPath) != "" {
			configPath = strings.TrimSpace(envPath)
			configSet = true
		}
	}
	if !configSet {
		configPath = filepath.Join(projectDefault, config.DefaultFileName)
	}

	serveConfig, err := config.LoadServeConfig(configPath, !configSet)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := config.ApplyEnvironment(&serveConfig, os.LookupEnv); err != nil {
		log.Fatalf("config environment: %v", err)
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	projectFlag := fs.String("project", projectDefault,
		"workspace directory (holds sub-project folders, each with its own graph.yaml)")
	fs.String("config", configPath, "server YAML config file (default: <project>/nodevas.yaml)")
	port := fs.Int("port", serveConfig.Port, "listen port")
	listen := fs.String("listen", serveConfig.Listen,
		"listen address; anything other than loopback requires accounts (nodevas user add)")
	hostname := fs.String("hostname", serveConfig.Hostname,
		"comma-separated further Host names this server answers on (the public DNS name in front of a proxy)")
	behindProxy := fs.Bool("behind-proxy", serveConfig.BehindProxy,
		"trust X-Forwarded-Proto from a terminating reverse proxy")
	trustedProxy := fs.String("trusted-proxy", serveConfig.TrustedProxy,
		"comma-separated proxy IPs/CIDRs allowed to supply forwarding headers")
	certFile := fs.String("tls-cert", serveConfig.TLSCert, "TLS certificate file (PEM)")
	keyFile := fs.String("tls-key", serveConfig.TLSKey, "TLS private key file (PEM)")
	allowPlaintext := fs.Bool("allow-plaintext", serveConfig.AllowPlaintext,
		"serve a networked listener over plain HTTP (passwords and session cookies travel in the clear)")
	maxActiveUsers := fs.Int("max-active-users", serveConfig.MaxActiveUsers,
		"how many distinct accounts may be signed in at once; 0 means no limit. Counts people, not devices: one person on a phone and a laptop is one")
	smtpHost := fs.String("smtp-host", serveConfig.SMTP.Host, "SMTP host for one-time passcodes")
	smtpPort := fs.Int("smtp-port", serveConfig.SMTP.Port, "SMTP port: 587 for STARTTLS, 465 for implicit TLS")
	smtpUser := fs.String("smtp-user", serveConfig.SMTP.User, "SMTP username")
	smtpFrom := fs.String("smtp-from", serveConfig.SMTP.From, "From address for passcode mail, e.g. \"Nodevas <no-reply@example.com>\"")
	smtpSecurity := fs.String("smtp-security", serveConfig.SMTP.Security,
		"starttls, implicit, or none (none is refused unless the host is loopback)")
	logLevel := fs.String("log-level", serveConfig.Logging.Level, "debug, info, warn, or error")
	logFormat := fs.String("log-format", serveConfig.Logging.Format,
		"json for a log shipper (Elastic Common Schema field names), text for a terminal")

	abusePreAuthGlobalRate := fs.Float64("abuse-pre-auth-global-rate", serveConfig.Abuse.PreAuthGlobalRate, "pre-auth global rate limit (req/s)")
	abusePreAuthGlobalBurst := fs.Float64("abuse-pre-auth-global-burst", serveConfig.Abuse.PreAuthGlobalBurst, "pre-auth global burst capacity")
	abusePreAuthIPRate := fs.Float64("abuse-pre-auth-ip-rate", serveConfig.Abuse.PreAuthIPRate, "pre-auth per-IP rate limit (req/s)")
	abusePreAuthIPBurst := fs.Float64("abuse-pre-auth-ip-burst", serveConfig.Abuse.PreAuthIPBurst, "pre-auth per-IP burst capacity")

	abuseReadGlobalRate := fs.Float64("abuse-read-global-rate", serveConfig.Abuse.ReadGlobalRate, "read global rate limit (req/s)")
	abuseReadGlobalBurst := fs.Float64("abuse-read-global-burst", serveConfig.Abuse.ReadGlobalBurst, "read global burst capacity")
	abuseReadIPRate := fs.Float64("abuse-read-ip-rate", serveConfig.Abuse.ReadIPRate, "read per-IP rate limit (req/s)")
	abuseReadIPBurst := fs.Float64("abuse-read-ip-burst", serveConfig.Abuse.ReadIPBurst, "read per-IP burst capacity")
	abuseReadActorRate := fs.Float64("abuse-read-actor-rate", serveConfig.Abuse.ReadActorRate, "read per-actor rate limit (req/s)")
	abuseReadActorBurst := fs.Float64("abuse-read-actor-burst", serveConfig.Abuse.ReadActorBurst, "read per-actor burst capacity")

	abuseWriteGlobalRate := fs.Float64("abuse-write-global-rate", serveConfig.Abuse.WriteGlobalRate, "write global rate limit (req/s)")
	abuseWriteGlobalBurst := fs.Float64("abuse-write-global-burst", serveConfig.Abuse.WriteGlobalBurst, "write global burst capacity")
	abuseWriteIPRate := fs.Float64("abuse-write-ip-rate", serveConfig.Abuse.WriteIPRate, "write per-IP rate limit (req/s)")
	abuseWriteIPBurst := fs.Float64("abuse-write-ip-burst", serveConfig.Abuse.WriteIPBurst, "write per-IP burst capacity")
	abuseWriteActorRate := fs.Float64("abuse-write-actor-rate", serveConfig.Abuse.WriteActorRate, "write per-actor rate limit (req/s)")
	abuseWriteActorBurst := fs.Float64("abuse-write-actor-burst", serveConfig.Abuse.WriteActorBurst, "write per-actor burst capacity")

	abuseExpensiveGlobalRate := fs.Float64("abuse-expensive-global-rate", serveConfig.Abuse.ExpensiveGlobalRate, "expensive routes global rate limit (req/s)")
	abuseExpensiveGlobalBurst := fs.Float64("abuse-expensive-global-burst", serveConfig.Abuse.ExpensiveGlobalBurst, "expensive routes global burst capacity")
	abuseExpensiveIPRate := fs.Float64("abuse-expensive-ip-rate", serveConfig.Abuse.ExpensiveIPRate, "expensive routes per-IP rate limit (req/s)")
	abuseExpensiveIPBurst := fs.Float64("abuse-expensive-ip-burst", serveConfig.Abuse.ExpensiveIPBurst, "expensive routes per-IP burst capacity")
	abuseExpensiveActorRate := fs.Float64("abuse-expensive-actor-rate", serveConfig.Abuse.ExpensiveActorRate, "expensive routes per-actor rate limit (req/s)")
	abuseExpensiveActorBurst := fs.Float64("abuse-expensive-actor-burst", serveConfig.Abuse.ExpensiveActorBurst, "expensive routes per-actor burst capacity")

	abuseHeavyGlobalRate := fs.Float64("abuse-heavy-global-rate", serveConfig.Abuse.HeavyGlobalRate, "heavy routes global rate limit (req/s)")
	abuseHeavyGlobalBurst := fs.Float64("abuse-heavy-global-burst", serveConfig.Abuse.HeavyGlobalBurst, "heavy routes global burst capacity")
	abuseHeavyIPRate := fs.Float64("abuse-heavy-ip-rate", serveConfig.Abuse.HeavyIPRate, "heavy routes per-IP rate limit (req/s)")
	abuseHeavyIPBurst := fs.Float64("abuse-heavy-ip-burst", serveConfig.Abuse.HeavyIPBurst, "heavy routes per-IP burst capacity")
	abuseHeavyActorRate := fs.Float64("abuse-heavy-actor-rate", serveConfig.Abuse.HeavyActorRate, "heavy routes per-actor rate limit (req/s)")
	abuseHeavyActorBurst := fs.Float64("abuse-heavy-actor-burst", serveConfig.Abuse.HeavyActorBurst, "heavy routes per-actor burst capacity")

	abuseWSUpgradeGlobalRate := fs.Float64("abuse-ws-upgrade-global-rate", serveConfig.Abuse.WSUpgradeGlobalRate, "ws upgrade global rate limit (req/s)")
	abuseWSUpgradeGlobalBurst := fs.Float64("abuse-ws-upgrade-global-burst", serveConfig.Abuse.WSUpgradeGlobalBurst, "ws upgrade global burst capacity")
	abuseWSUpgradeIPRate := fs.Float64("abuse-ws-upgrade-ip-rate", serveConfig.Abuse.WSUpgradeIPRate, "ws upgrade per-IP rate limit (req/s)")
	abuseWSUpgradeIPBurst := fs.Float64("abuse-ws-upgrade-ip-burst", serveConfig.Abuse.WSUpgradeIPBurst, "ws upgrade per-IP burst capacity")
	abuseWSUpgradeActorRate := fs.Float64("abuse-ws-upgrade-actor-rate", serveConfig.Abuse.WSUpgradeActorRate, "ws upgrade per-actor rate limit (req/s)")
	abuseWSUpgradeActorBurst := fs.Float64("abuse-ws-upgrade-actor-burst", serveConfig.Abuse.WSUpgradeActorBurst, "ws upgrade per-actor burst capacity")

	abuseMaxConcurrentHeavy := fs.Int("abuse-max-concurrent-heavy", serveConfig.Abuse.MaxConcurrentHeavy, "max concurrent heavy operations")
	abuseMaxConcurrentDOCX := fs.Int("abuse-max-concurrent-docx", serveConfig.Abuse.MaxConcurrentDOCX, "max concurrent DOCX operations")
	abuseMaxConcurrentRemote := fs.Int("abuse-max-concurrent-remote", serveConfig.Abuse.MaxConcurrentRemote, "max concurrent remote sync operations")
	_ = fs.Parse(args)

	// The flag values now represent the complete precedence chain:
	// defaults < YAML < environment < CLI.
	serveConfig.Port = *port
	serveConfig.Listen = *listen
	serveConfig.Hostname = *hostname
	serveConfig.BehindProxy = *behindProxy
	serveConfig.TrustedProxy = *trustedProxy
	serveConfig.TLSCert = *certFile
	serveConfig.TLSKey = *keyFile
	serveConfig.AllowPlaintext = *allowPlaintext
	serveConfig.MaxActiveUsers = *maxActiveUsers
	serveConfig.SMTP.Host = *smtpHost
	serveConfig.SMTP.Port = *smtpPort
	serveConfig.SMTP.User = *smtpUser
	serveConfig.SMTP.From = *smtpFrom
	serveConfig.SMTP.Security = *smtpSecurity
	serveConfig.Logging.Level = *logLevel
	serveConfig.Logging.Format = *logFormat

	serveConfig.Abuse.PreAuthGlobalRate = *abusePreAuthGlobalRate
	serveConfig.Abuse.PreAuthGlobalBurst = *abusePreAuthGlobalBurst
	serveConfig.Abuse.PreAuthIPRate = *abusePreAuthIPRate
	serveConfig.Abuse.PreAuthIPBurst = *abusePreAuthIPBurst
	serveConfig.Abuse.ReadGlobalRate = *abuseReadGlobalRate
	serveConfig.Abuse.ReadGlobalBurst = *abuseReadGlobalBurst
	serveConfig.Abuse.ReadIPRate = *abuseReadIPRate
	serveConfig.Abuse.ReadIPBurst = *abuseReadIPBurst
	serveConfig.Abuse.ReadActorRate = *abuseReadActorRate
	serveConfig.Abuse.ReadActorBurst = *abuseReadActorBurst
	serveConfig.Abuse.WriteGlobalRate = *abuseWriteGlobalRate
	serveConfig.Abuse.WriteGlobalBurst = *abuseWriteGlobalBurst
	serveConfig.Abuse.WriteIPRate = *abuseWriteIPRate
	serveConfig.Abuse.WriteIPBurst = *abuseWriteIPBurst
	serveConfig.Abuse.WriteActorRate = *abuseWriteActorRate
	serveConfig.Abuse.WriteActorBurst = *abuseWriteActorBurst
	serveConfig.Abuse.ExpensiveGlobalRate = *abuseExpensiveGlobalRate
	serveConfig.Abuse.ExpensiveGlobalBurst = *abuseExpensiveGlobalBurst
	serveConfig.Abuse.ExpensiveIPRate = *abuseExpensiveIPRate
	serveConfig.Abuse.ExpensiveIPBurst = *abuseExpensiveIPBurst
	serveConfig.Abuse.ExpensiveActorRate = *abuseExpensiveActorRate
	serveConfig.Abuse.ExpensiveActorBurst = *abuseExpensiveActorBurst
	serveConfig.Abuse.HeavyGlobalRate = *abuseHeavyGlobalRate
	serveConfig.Abuse.HeavyGlobalBurst = *abuseHeavyGlobalBurst
	serveConfig.Abuse.HeavyIPRate = *abuseHeavyIPRate
	serveConfig.Abuse.HeavyIPBurst = *abuseHeavyIPBurst
	serveConfig.Abuse.HeavyActorRate = *abuseHeavyActorRate
	serveConfig.Abuse.HeavyActorBurst = *abuseHeavyActorBurst
	serveConfig.Abuse.WSUpgradeGlobalRate = *abuseWSUpgradeGlobalRate
	serveConfig.Abuse.WSUpgradeGlobalBurst = *abuseWSUpgradeGlobalBurst
	serveConfig.Abuse.WSUpgradeIPRate = *abuseWSUpgradeIPRate
	serveConfig.Abuse.WSUpgradeIPBurst = *abuseWSUpgradeIPBurst
	serveConfig.Abuse.WSUpgradeActorRate = *abuseWSUpgradeActorRate
	serveConfig.Abuse.WSUpgradeActorBurst = *abuseWSUpgradeActorBurst
	serveConfig.Abuse.MaxConcurrentHeavy = *abuseMaxConcurrentHeavy
	serveConfig.Abuse.MaxConcurrentDOCX = *abuseMaxConcurrentDOCX
	serveConfig.Abuse.MaxConcurrentRemote = *abuseMaxConcurrentRemote
	if err := serveConfig.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	// Logging is set up before anything else can fail, so a startup error is
	// itself a structured record rather than the one line that escapes the
	// pipeline.
	logger, err := logging.Setup(os.Stderr, logging.Config{
		Level:   *logLevel,
		Format:  *logFormat,
		Service: "nodevas",
	})
	if err != nil {
		log.Fatalf("logging: %v", err)
	}

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		log.Fatalf("project dir: %v", err)
	}
	hostNames := hostList(*hostname)
	if err := validateServeFlags(
		*listen, *port, hostNames, *certFile, *keyFile, *behindProxy, *allowPlaintext,
	); err != nil {
		log.Fatal(err)
	}
	remote := isRemoteDeployment(*listen, *behindProxy)
	useTLS := *certFile != ""
	proxyOnlyTransport := *behindProxy && isLoopbackHost(*listen)

	hub, pm, database := bootstrapWorkspace(root)
	defer pm.Close()
	defer database.Close()

	distFS, ok := web.Dist()
	if !ok {
		distFS = nil
		log.Print("frontend build not embedded; use the Vite dev server for the UI")
	}
	srv := server.New(pm, hub, distFS)
	srv.SetAbuseOptions(server.AbuseGuardOptions{
		PreAuthGlobalRate:    serveConfig.Abuse.PreAuthGlobalRate,
		PreAuthGlobalBurst:   serveConfig.Abuse.PreAuthGlobalBurst,
		PreAuthIPRate:        serveConfig.Abuse.PreAuthIPRate,
		PreAuthIPBurst:       serveConfig.Abuse.PreAuthIPBurst,
		ReadGlobalRate:       serveConfig.Abuse.ReadGlobalRate,
		ReadGlobalBurst:      serveConfig.Abuse.ReadGlobalBurst,
		ReadIPRate:           serveConfig.Abuse.ReadIPRate,
		ReadIPBurst:          serveConfig.Abuse.ReadIPBurst,
		ReadActorRate:        serveConfig.Abuse.ReadActorRate,
		ReadActorBurst:       serveConfig.Abuse.ReadActorBurst,
		WriteGlobalRate:      serveConfig.Abuse.WriteGlobalRate,
		WriteGlobalBurst:     serveConfig.Abuse.WriteGlobalBurst,
		WriteIPRate:          serveConfig.Abuse.WriteIPRate,
		WriteIPBurst:         serveConfig.Abuse.WriteIPBurst,
		WriteActorRate:       serveConfig.Abuse.WriteActorRate,
		WriteActorBurst:      serveConfig.Abuse.WriteActorBurst,
		ExpensiveGlobalRate:  serveConfig.Abuse.ExpensiveGlobalRate,
		ExpensiveGlobalBurst: serveConfig.Abuse.ExpensiveGlobalBurst,
		ExpensiveIPRate:      serveConfig.Abuse.ExpensiveIPRate,
		ExpensiveIPBurst:     serveConfig.Abuse.ExpensiveIPBurst,
		ExpensiveActorRate:   serveConfig.Abuse.ExpensiveActorRate,
		ExpensiveActorBurst:  serveConfig.Abuse.ExpensiveActorBurst,
		HeavyGlobalRate:      serveConfig.Abuse.HeavyGlobalRate,
		HeavyGlobalBurst:     serveConfig.Abuse.HeavyGlobalBurst,
		HeavyIPRate:          serveConfig.Abuse.HeavyIPRate,
		HeavyIPBurst:         serveConfig.Abuse.HeavyIPBurst,
		HeavyActorRate:       serveConfig.Abuse.HeavyActorRate,
		HeavyActorBurst:      serveConfig.Abuse.HeavyActorBurst,
		WSUpgradeGlobalRate:  serveConfig.Abuse.WSUpgradeGlobalRate,
		WSUpgradeGlobalBurst: serveConfig.Abuse.WSUpgradeGlobalBurst,
		WSUpgradeIPRate:      serveConfig.Abuse.WSUpgradeIPRate,
		WSUpgradeIPBurst:     serveConfig.Abuse.WSUpgradeIPBurst,
		WSUpgradeActorRate:   serveConfig.Abuse.WSUpgradeActorRate,
		WSUpgradeActorBurst:  serveConfig.Abuse.WSUpgradeActorBurst,
		MaxConcurrentHeavy:   serveConfig.Abuse.MaxConcurrentHeavy,
		MaxConcurrentDOCX:    serveConfig.Abuse.MaxConcurrentDOCX,
		MaxConcurrentRemote:  serveConfig.Abuse.MaxConcurrentRemote,
	})
	slog.Info("abuse limiters configured",
		slog.Float64("pre_auth_rate", serveConfig.Abuse.PreAuthGlobalRate),
		slog.Float64("read_rate", serveConfig.Abuse.ReadGlobalRate),
		slog.Float64("write_rate", serveConfig.Abuse.WriteGlobalRate),
		slog.Float64("expensive_rate", serveConfig.Abuse.ExpensiveGlobalRate),
		slog.Float64("heavy_rate", serveConfig.Abuse.HeavyGlobalRate),
		slog.Float64("ws_upgrade_rate", serveConfig.Abuse.WSUpgradeGlobalRate),
	)
	srv.UseAudit(audit.New(database))
	// The wildcard-without-a-name case is refused above for a remote
	// deployment; this catches the rest, and keeps the guarantee in the server
	// package rather than only in this one call site.
	if err := srv.UseListenAddress(*listen, *port, hostNames); err != nil {
		log.Fatalf("listen address: %v", err)
	}
	// The header only means anything when a proxy in front of us rewrites it;
	// believed by default it is a free HTTPS claim for any plaintext client.
	if *behindProxy {
		if err := auth.SetTrustedProxyCIDRs(strings.Split(*trustedProxy, ",")); err != nil {
			log.Fatalf("trusted proxy: %v", err)
		}
	} else {
		auth.TrustForwardedProto(false)
	}

	if remote {
		// Share the handle rather than opening a second one: SQLite admits one
		// writer, and two pools would queue against each other for it.
		users := auth.NewUserStoreDB(database)
		if err := users.EnsureAdmin(); err != nil {
			log.Fatalf("accounts: %v", err)
		}
		// No request is behind startup or the `nodevas user` commands, so the
		// account queries below get a background context: there is nobody who
		// could disconnect and nothing that should cancel them.
		accountCtx := context.Background()
		if users.Count(accountCtx) == 0 {
			log.Fatalf(
				"refusing to serve %s with no accounts: create one first with "+
					"`nodevas user add --project %q --user <name>`", *listen, root)
		}
		srv.UseAccounts(users)
		srv.SetMaxActiveUsers(*maxActiveUsers)
		log.Printf("accounts enabled (%d users)", users.Count(accountCtx))
		if *maxActiveUsers > 0 {
			log.Printf("at most %d people may be signed in at once", *maxActiveUsers)
		}

		// The visitor credential lives in the workspace database and is read on
		// every sign-in attempt, so `nodevas visitor on|off` changes it without
		// a restart. Nothing is configured here; this only reports what the
		// database already says.
		//
		// Said loudly on purpose. One shared credential means everyone who has
		// it can read everything on this server, and an operator who turned it
		// on months ago should be reminded it is still on.
		if pin, _, err := users.VisitorCredential(accountCtx); err == nil && pin != "" {
			log.Printf(
				"WARNING: visitor access is ON (pin %q). Anyone who knows it can read every "+
					"project and attachment on this server, and may copy or save anything visible. "+
					"It grants no writes, uploads, bulk exports, administration or host access. "+
					"Turn it off with `nodevas visitor off --project %q`.", pin, root)
		}

		// Sign-in needs two factors, and the second one arrives by mail. A
		// server with accounts and no relay admits nobody, so say that at
		// startup rather than letting it look like mail that never came.
		if strings.TrimSpace(*smtpHost) == "" {
			log.Print(
				"WARNING: no --smtp-host, so no one-time passcodes can be sent and nobody can sign in. " +
					"Configure outgoing mail, or serve on loopback where accounts are not used.")
		} else {
			// The password comes from the environment, never a flag: a flag is
			// in the process list of every other user on the machine.
			smtpPassword, err := secretEnvironment("NODEVAS_SMTP_PASSWORD")
			if err != nil {
				log.Fatalf("smtp password: %v", err)
			}
			sender, err := mail.New(mail.Config{
				Host:     *smtpHost,
				Port:     *smtpPort,
				Username: *smtpUser,
				Password: smtpPassword,
				From:     *smtpFrom,
				Security: *smtpSecurity,
			})
			if err != nil {
				log.Fatalf("smtp: %v", err)
			}
			srv.UseMailer(sender)
			log.Printf("passcode mail via %s:%d as %s", *smtpHost, *smtpPort, *smtpFrom)
		}
	}

	addr := net.JoinHostPort(*listen, fmt.Sprint(*port))
	httpServer := &http.Server{
		Addr: addr,
		// The request log sits outermost so it sees the status every other
		// layer settled on, including the ones that refuse a request before it
		// reaches a route.
		Handler: logging.MiddlewareWithOptions(logging.Options{
			Logger: logger,
			// The trusted-proxy rules live in the auth package; parsing
			// forwarding headers a second time here would be a second answer
			// to the question of who the client is.
			ClientIP: auth.ClientIP,
		})(protectHTTPTransport(srv.Handler())),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	if useTLS {
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	log.Printf("nodevas serving %s on %s://%s", root, scheme, addr)
	if remote && !useTLS && !proxyOnlyTransport {
		log.Print("WARNING: serving over plain HTTP; passwords and session cookies are visible on the network")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		if useTLS {
			errCh <- httpServer.ListenAndServeTLS(*certFile, *keyFile)
			return
		}
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %v", err)
			_ = httpServer.Close()
		}
		backupCtx, backupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := srv.Shutdown(backupCtx); err != nil {
			log.Printf("final backup: %v", err)
		}
		backupCancel()
	}
}

const (
	requestReadTimeout   = 60 * time.Second
	responseWriteTimeout = 10 * time.Minute
)

// protectHTTPTransport places absolute per-request body and response
// deadlines around ordinary HTTP traffic. WebSockets get their own frame,
// rate, ping, and write deadlines after upgrade, so they are excluded here.
func protectHTTPTransport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/ws" {
			next.ServeHTTP(writer, request)
			return
		}
		if request.URL.Path == "/api/auth/login" && request.Body != nil {
			request.Body = http.MaxBytesReader(writer, request.Body, auth.MaxLoginBodyBytes)
		}
		controller := http.NewResponseController(writer)
		_ = controller.SetReadDeadline(time.Now().Add(requestReadTimeout))
		_ = controller.SetWriteDeadline(time.Now().Add(responseWriteTimeout))
		defer func() {
			_ = controller.SetReadDeadline(time.Time{})
			_ = controller.SetWriteDeadline(time.Time{})
		}()
		next.ServeHTTP(writer, request)
	})
}
