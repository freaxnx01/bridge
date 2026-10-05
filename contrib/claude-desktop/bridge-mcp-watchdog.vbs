' Silent launcher for bridge-mcp-watchdog.ps1.
'
' Why this exists: the scheduled task originally ran pwsh.exe directly. pwsh is a
' console app, so Windows allocates a console window for it in the interactive
' session before -WindowStyle Hidden is parsed -- producing a visible window flash
' every 5 minutes. wscript.exe is a GUI host and never allocates a console, so
' WshShell.Run with intWindowStyle=0 is genuinely invisible.
'
' bWaitOnReturn=True keeps this wrapper alive for the script's lifetime so the task's
' ExecutionTimeLimit and MultipleInstances=IgnoreNew still govern the real work.
'
' The .ps1 is expected next to this file.

Dim sh, fso, scriptDir, cmd
Set sh = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
scriptDir = fso.GetParentFolderName(WScript.ScriptFullName)

cmd = """C:\Program Files\PowerShell\7\pwsh.exe""" & _
      " -NoProfile -NonInteractive -ExecutionPolicy Bypass" & _
      " -File """ & scriptDir & "\bridge-mcp-watchdog.ps1"""

WScript.Quit sh.Run(cmd, 0, True)
