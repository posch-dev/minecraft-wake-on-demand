Dim dir
dir = CreateObject("Scripting.FileSystemObject").GetParentFolderName(WScript.ScriptFullName)
CreateObject("WScript.Shell").Run """" & dir & "\mc-wol-proxy.exe""", 0, False
