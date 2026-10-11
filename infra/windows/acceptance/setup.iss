#ifndef SourceFile
  #error SourceFile must point to the acceptance payload
#endif

#ifndef AcceptanceVersion
  #define AcceptanceVersion "0.0.0"
#endif

[Setup]
AppId={{D22B03B2-EF4C-49B8-9A18-A87453F55342}
AppName=Vela Windows Toolchain Acceptance
AppVersion={#AcceptanceVersion}
DefaultDirName={autopf}\VelaToolchainAcceptance
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
Compression=lzma2
CreateAppDir=yes
DisableProgramGroupPage=yes
OutputBaseFilename=vela-toolchain-acceptance
PrivilegesRequired=lowest
SolidCompression=yes
Uninstallable=no
WizardStyle=modern

[Files]
Source: "{#SourceFile}"; DestDir: "{app}"; DestName: "acceptance-payload.md"; Flags: ignoreversion
