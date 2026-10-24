package agentsetup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openCodeToolTimeoutMs is the MCP request timeout OpenCode gives the nodevas
// server; it applies to tool calls too, and check_inbox and ask_user
// long-poll.
const openCodeToolTimeoutMs = 660000

// openCodePluginMarker starts the plugin file setup writes. A file at that
// path without it is someone else's and is never replaced or removed.
const openCodePluginMarker = "// nodevas setup-agent: OpenCode hooks for the Nodevas agent network."

// openCode wires OpenCode: the MCP server in its global opencode.json, and
// hooks as a plugin in its global plugins folder, which OpenCode loads by
// itself. OpenCode has no command hooks; the plugin runs `nodevas hook
// opencode <event>` for the session and tool events it is told about.
func (s *setup) openCode() error {
	dir := s.env.openCodeHome()
	if err := s.openCodeMCP(filepath.Join(dir, "opencode.json")); err != nil {
		return err
	}
	return s.openCodePlugin(filepath.Join(dir, "plugins", ServerName+".js"))
}

// openCodeMCP sets (or removes) mcp.nodevas in opencode.json. OpenCode also
// reads opencode.jsonc and config.json from the same folder and merges them,
// so the entry can live in opencode.json whichever the person uses.
func (s *setup) openCodeMCP(path string) error {
	doc, existed, err := readJSON(path)
	if err != nil {
		return err
	}
	_, hadServers := doc.get("mcp")
	servers, err := doc.child("mcp")
	if err != nil {
		return err
	}
	serversHadKeys := len(servers.keys) > 0
	if s.opts.Remove {
		servers.del(ServerName)
	} else {
		command := []any{s.env.Exe}
		for _, a := range s.mcpArgs() {
			command = append(command, a)
		}
		want := obj(
			"type", "local",
			"command", command,
			"enabled", true,
			"timeout", openCodeToolTimeoutMs,
			"environment", obj("NODEVAS_AGENT", OpenCode),
		)
		if cur, ok := servers.get(ServerName); !ok || !sameJSON(cur, want) {
			servers.set(ServerName, want)
		}
	}
	if len(servers.keys) == 0 && (!hadServers || serversHadKeys) {
		doc.del("mcp")
	}
	if !existed && !s.opts.Remove {
		// A new file says what it is, as OpenCode's own do.
		withSchema := obj("$schema", "https://opencode.ai/config.json")
		for _, k := range doc.keys {
			withSchema.set(k, doc.vals[k])
		}
		doc = withSchema
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	return nil
}

// openCodePlugin writes (or removes) the hooks plugin. The file is wholly
// nodevas's, so there is nothing to merge and no backup to take.
func (s *setup) openCodePlugin(path string) error {
	old, existed, err := readFile(path)
	if err != nil {
		return err
	}
	if existed && !bytes.HasPrefix(bytes.TrimPrefix(old, []byte("\xef\xbb\xbf")), []byte(openCodePluginMarker)) {
		return fmt.Errorf("%s was not written by nodevas; not touching it", tilde(s.env.Home, path))
	}
	if s.opts.Remove {
		if !existed {
			s.changed(path, false)
			return nil
		}
		if !s.opts.DryRun {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		s.changed(path, true)
		return nil
	}
	content, err := openCodePluginSource(s.env.Exe, s.hookFlags())
	if err != nil {
		return err
	}
	if existed && bytes.Equal(old, content) {
		s.changed(path, false)
		return nil
	}
	if !s.opts.DryRun {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp-nodevas"
		if err := os.WriteFile(tmp, content, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	s.changed(path, true)
	if !existed {
		s.note("OpenCode loads the plugin when it next starts")
	}
	return nil
}

// openCodePluginSource is the plugin's JavaScript, with exe and the hook
// flags written in as JSON string literals.
func openCodePluginSource(exe string, flags []string) ([]byte, error) {
	lit := func(v any) (string, error) {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return "", err
		}
		return strings.TrimSpace(b.String()), nil
	}
	exeLit, err := lit(exe)
	if err != nil {
		return nil, err
	}
	flagsLit, err := lit(flags)
	if err != nil {
		return nil, err
	}
	src := strings.NewReplacer(
		"@@MARKER@@", openCodePluginMarker,
		"@@EXE@@", exeLit,
		"@@FLAGS@@", flagsLit,
	).Replace(openCodePluginTemplate)
	return []byte(src), nil
}

// openCodePluginTemplate is the plugin. It never throws and never holds the
// agent up for long: the hook it runs is bounded by its own timeouts, and
// only chat.message waits for it (for mail to put in front of the model).
// Sessions a runner started (NODEVAS_RUN_ID) are the runner's, not the
// network's, and subagent sessions belong to the session that started them,
// tokens included: the plugin counts every assistant message's tokens and
// cost against its top session and passes the totals, per model, to the stop
// and session-end hooks (OpenCode keeps no transcript file to read). A
// session first seen after OpenCode restarted is counted from its stored
// messages once, so the totals stay totals.
const openCodePluginTemplate = `@@MARKER@@
// Written by ` + "`nodevas setup-agent`" + `, removed by ` + "`nodevas setup-agent --remove`" + `;
// edits here are lost the next time setup runs.
import { spawn } from "node:child_process";

const NODEVAS = @@EXE@@;
const FLAGS = @@FLAGS@@;
const WAIT_MS = 10000;
const SEED_MS = 3000;

function hook(event, payload, wait) {
  return new Promise((resolve) => {
    let out = "";
    let done = false;
    let timer = null;
    const finish = () => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      resolve(out.trim());
    };
    let child;
    try {
      child = spawn(NODEVAS, ["hook", "opencode", event, ...FLAGS], {
        stdio: ["pipe", wait ? "pipe" : "ignore", "ignore"],
        windowsHide: true,
      });
    } catch {
      finish();
      return;
    }
    child.on("error", finish);
    child.on("close", finish);
    if (wait && child.stdout) {
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk) => {
        if (out.length < 200000) out += chunk;
      });
      timer = setTimeout(() => {
        try { child.kill(); } catch {}
        finish();
      }, WAIT_MS);
    }
    try {
      child.stdin.on("error", () => {});
      child.stdin.end(JSON.stringify(payload));
    } catch {}
    if (!wait) {
      try { child.unref(); } catch {}
      finish();
    }
  });
}

const num = (v) => (typeof v === "number" && isFinite(v) && v > 0 ? v : 0);

export const NodevasPlugin = async ({ directory, client }) => {
  if (process.env.NODEVAS_RUN_ID) return {};
  const cwd = directory || process.cwd();
  // parents maps a subagent session to the session that started it.
  const parents = new Map();
  const own = (sessionID) => typeof sessionID === "string" && sessionID !== "" && !parents.has(sessionID);
  const top = (sessionID) => {
    let id = sessionID;
    for (let i = 0; i < 32 && parents.has(id); i++) id = parents.get(id);
    return id;
  };
  // messages: assistant message id -> { session, model, tokens, cost }.
  const messages = new Map();
  const seeded = new Set();
  const count = (info, session) => {
    if (!info || info.role !== "assistant" || typeof info.id !== "string") return;
    const t = info.tokens || {};
    const cache = t.cache || {};
    const model = info.modelID ? (info.providerID ? info.providerID + "/" + info.modelID : info.modelID) : "unknown";
    messages.set(info.id, {
      session: session || top(info.sessionID),
      model,
      inputTokens: num(t.input),
      outputTokens: num(t.output) + num(t.reasoning),
      cacheReadTokens: num(cache.read),
      cacheWriteTokens: num(cache.write),
      costUsd: num(info.cost),
    });
  };
  const seed = async (sessionID) => {
    if (seeded.has(sessionID)) return;
    seeded.add(sessionID);
    try {
      if (!client || !client.session || typeof client.session.messages !== "function") return;
      let timer = null;
      const result = await Promise.race([
        client.session.messages({ path: { id: sessionID } }),
        new Promise((resolve) => { timer = setTimeout(() => resolve(null), SEED_MS); }),
      ]);
      if (timer) clearTimeout(timer);
      const list = (result && Array.isArray(result.data) && result.data) || (Array.isArray(result) && result) || [];
      for (const item of list) {
        const info = (item && item.info) || item;
        if (info && !messages.has(info.id)) count(info, sessionID);
      }
    } catch {}
  };
  const usage = async (sessionID) => {
    await seed(sessionID);
    const byModel = new Map();
    for (const m of messages.values()) {
      if (m.session !== sessionID) continue;
      const u = byModel.get(m.model) || { model: m.model, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0 };
      u.inputTokens += m.inputTokens;
      u.outputTokens += m.outputTokens;
      u.cacheReadTokens += m.cacheReadTokens;
      u.cacheWriteTokens += m.cacheWriteTokens;
      u.costUsd += m.costUsd;
      byModel.set(m.model, u);
    }
    return [...byModel.values()];
  };
  return {
    event: async ({ event }) => {
      try {
        const props = (event && event.properties) || {};
        const info = props.info || {};
        switch (event && event.type) {
          case "session.created":
            if (info.parentID) {
              parents.set(info.id, info.parentID);
            } else if (own(info.id)) {
              seeded.add(info.id);
              hook("session-start", { session_id: info.id, cwd: info.directory || cwd }, false);
            }
            break;
          case "message.updated":
            count(info);
            break;
          case "session.idle":
            if (own(props.sessionID)) {
              hook("stop", { session_id: props.sessionID, cwd, usage: await usage(props.sessionID) }, false);
            }
            break;
          case "session.deleted":
            if (own(info.id)) hook("session-end", { session_id: info.id, cwd, usage: await usage(info.id) }, false);
            parents.delete(info.id);
            for (const [id, m] of messages) if (m.session === info.id) messages.delete(id);
            seeded.delete(info.id);
            break;
        }
      } catch {}
    },
    "chat.message": async (input, output) => {
      try {
        if (!own(input.sessionID)) return;
        const payload = { session_id: input.sessionID, cwd };
        const model = input.model || {};
        if (model.modelID) payload.model = model.providerID ? model.providerID + "/" + model.modelID : model.modelID;
        const text = await hook("user-prompt-submit", payload, true);
        if (!text || !output || !Array.isArray(output.parts)) return;
        const last = output.parts[output.parts.length - 1] || {};
        const message = output.message || {};
        output.parts.push({
          id: (last.id || "prt_" + Date.now().toString(16)) + "nodevas",
          sessionID: message.sessionID || input.sessionID,
          messageID: message.id || input.messageID || last.messageID,
          type: "text",
          text,
          synthetic: true,
        });
      } catch {}
    },
    "tool.execute.after": async (input) => {
      try {
        if (!own(input.sessionID)) return;
        hook("post-tool-use", { session_id: input.sessionID, cwd, tool_name: input.tool, tool_input: input.args || {} }, false);
      } catch {}
    },
  };
};
`
