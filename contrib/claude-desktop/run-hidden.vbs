' Start a command with no visible window (SW_HIDE at creation), for scheduled tasks.
' pwsh -WindowStyle Hidden only hides its console after it has been created, and on this
' box the console stays visible. Usage: wscript.exe //B //NoLogo run-hidden.vbs <exe> [args...]
Set args = WScript.Arguments
If args.Count = 0 Then WScript.Quit 1
cmd = ""
For i = 0 To args.Count - 1
    a = args(i)
    If InStr(a, " ") > 0 Or a = "" Then a = """" & a & """"
    cmd = cmd & a & " "
Next
WScript.Quit CreateObject("WScript.Shell").Run(Trim(cmd), 0, True)
