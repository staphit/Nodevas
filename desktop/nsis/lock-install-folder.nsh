; Included by electron-builder's NSIS template (package.json build.nsis.include).
;
; An all-users install outside Program Files keeps the ACL of the folder it
; sits in. On a drive root or a data drive that lets every signed-in user
; replace Nodevas.exe, resources\bin\nodevas.exe or postgres.exe, which run
; as whoever opens Nodevas next. Installs made with the old folder page are
; like that, as are /D= installs, and every upgrade installs into the same
; folder again (the template reads InstallLocation back). So once the files
; are in place, an all-users folder outside Program Files gets the rules
; Program Files has: SYSTEM and administrators change it, users and app
; containers only read and run it. Program Files itself is left as it is.
;
; A just-me install outside the user's own Programs folder (the default,
; %LOCALAPPDATA%\Programs) is open the same way, and its upgrades run as
; that user. It gets the rules the user's profile has: SYSTEM,
; administrators and that user change it, nobody else even reads it.

!macro customInstall
  ${if} $installMode == "all"
    Push $0
    Push $1
    Push $2
    StrCpy $0 "lock"
    StrLen $1 "$PROGRAMFILES\"
    StrCpy $2 "$INSTDIR" $1
    ${if} $2 == "$PROGRAMFILES\"
      StrCpy $0 "keep"
    ${endif}
    StrLen $1 "$PROGRAMFILES64\"
    StrCpy $2 "$INSTDIR" $1
    ${if} $2 == "$PROGRAMFILES64\"
      StrCpy $0 "keep"
    ${endif}
    ${if} $0 == "lock"
      DetailPrint "Only administrators may change $INSTDIR"
      ; Administrators own every file, so a file someone else left here no
      ; longer lets them change its ACL.
      nsExec::ExecToLog '"$SYSDIR\icacls.exe" "$INSTDIR" /setowner *S-1-5-32-544 /T /C /Q'
      Pop $1
      ; Every file and folder drops ACEs of its own and inherits again...
      nsExec::ExecToLog '"$SYSDIR\icacls.exe" "$INSTDIR" /reset /T /C /Q'
      Pop $1
      ; ...from the install folder alone, which stops inheriting from its
      ; parent and grants what Program Files grants.
      nsExec::ExecToLog '"$SYSDIR\icacls.exe" "$INSTDIR" /inheritance:r /grant:r *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F *S-1-5-32-545:(OI)(CI)RX *S-1-15-2-1:(OI)(CI)RX *S-1-15-2-2:(OI)(CI)RX /Q'
      Pop $1
      ${if} $1 != 0
        DetailPrint "icacls could not restrict $INSTDIR (exit code $1)"
      ${endif}
    ${endif}
    Pop $2
    Pop $1
    Pop $0
  ${elseif} $installMode == "CurrentUser"
    Push $0
    Push $1
    Push $2
    Push $3
    Push $4
    StrCpy $0 "lock"
    StrLen $1 "$LOCALAPPDATA\Programs\"
    StrCpy $2 "$INSTDIR" $1
    ${if} $2 == "$LOCALAPPDATA\Programs\"
      StrCpy $0 "keep"
    ${endif}
    ${if} $0 == "lock"
      ; This account's SID, from the line whoami prints:
      ; "domain\name","S-1-5-21-..." (S-1-12-1-... for an Entra ID account).
      StrCpy $3 ""
      nsExec::ExecToStack '"$SYSDIR\whoami.exe" /user /fo csv /nh'
      Pop $1
      Pop $2
      ${if} $1 == 0
        StrCpy $1 0
        ${Do}
          StrCpy $4 $2 6 $1
          ${if} $4 == ',"S-1-'
            IntOp $1 $1 + 2
            StrCpy $3 $2 "" $1
            StrCpy $4 ""
          ${endif}
          IntOp $1 $1 + 1
        ${LoopUntil} $4 == ""
        ; Up to the closing quote: nothing that could end the argument.
        StrCpy $1 0
        ${Do}
          StrCpy $4 $3 1 $1
          ${if} $4 == '"'
          ${OrIf} $4 == " "
          ${OrIf} $4 == ","
          ${OrIf} $4 == "$\r"
          ${OrIf} $4 == "$\n"
            StrCpy $4 ""
          ${else}
            IntOp $1 $1 + 1
          ${endif}
        ${LoopUntil} $4 == ""
        StrCpy $3 $3 $1
      ${endif}
      StrLen $1 $3
      ${if} $1 < 8
        ; Without the SID the user could be locked out of their own
        ; install, and updates with them: the folder stays as it is.
        DetailPrint "Could not tell this account's SID; $INSTDIR keeps its permissions"
      ${else}
        DetailPrint "Only you and administrators may change $INSTDIR"
        ; Every file and folder drops ACEs of its own and inherits again...
        nsExec::ExecToLog '"$SYSDIR\icacls.exe" "$INSTDIR" /reset /T /C /Q'
        Pop $1
        ; ...from the install folder alone, which stops inheriting from its
        ; parent and grants what the user's own profile grants: SYSTEM,
        ; administrators and this account. Files another account made here
        ; keep their owner, who may still change them; an administrator's
        ; all-users install has no such gap.
        nsExec::ExecToLog '"$SYSDIR\icacls.exe" "$INSTDIR" /inheritance:r /grant:r *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F *$3:(OI)(CI)F /Q'
        Pop $1
        ${if} $1 != 0
          DetailPrint "icacls could not restrict $INSTDIR (exit code $1)"
        ${endif}
      ${endif}
    ${endif}
    Pop $4
    Pop $3
    Pop $2
    Pop $1
    Pop $0
  ${endif}
!macroend
