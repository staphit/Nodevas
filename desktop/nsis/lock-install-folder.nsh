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
; containers only read and run it. Program Files itself, and a just-me
; install, are left as they are.

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
  ${endif}
!macroend
