; Compile on Windows with Inno Setup 6. SourceDir is a GoReleaser ZIP extract.
#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef SourceDir
  #error SourceDir is required
#endif

[Setup]
AppId={{6E02999D-7F3C-43D9-9CE0-7B51704791CB}
AppName=ShadowTalk
AppVersion={#AppVersion}
AppPublisher=ShadowTalk contributors
AppPublisherURL=https://github.com/bacrehclb/ShadowTalk
DefaultDirName={localappdata}\Programs\ShadowTalk
DefaultGroupName=ShadowTalk
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern dynamic
Compression=lzma2
SolidCompression=yes
OutputBaseFilename=shadowtalk_{#AppVersion}_windows_amd64_setup
UninstallDisplayIcon={app}\shadowtalk.exe
ChangesEnvironment=yes
LicenseFile={#SourceDir}\LICENSE
DisableProgramGroupPage=yes
SetupLogging=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "addtopath"; Description: "Add ShadowTalk to the current user's PATH (open a new terminal afterwards)"

[Files]
Source: "{#SourceDir}\shadowtalk.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\README*.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\docs\*"; DestDir: "{app}\docs"; Flags: ignoreversion recursesubdirs

[Icons]
Name: "{group}\ShadowTalk terminal"; Filename: "{cmd}"; Parameters: "/K """"{app}\shadowtalk.exe"" --help"""; WorkingDir: "{app}"
Name: "{group}\Uninstall ShadowTalk"; Filename: "{uninstallexe}"

[Code]
const OwnerKey = 'Software\ShadowTalk\Installer';

// Work with complete PATH entries and preserve changes made by other programs.
function WithoutEntry(Value, Entry: String): String;
var Item, Normalized: String; Split: Integer; HasItem: Boolean;
begin
  Result := '';
  HasItem := False;
  repeat
    Split := Pos(';', Value);
    if Split = 0 then begin Item := Value; Value := ''; end
    else begin Item := Copy(Value, 1, Split - 1); Delete(Value, 1, Split); end;
    Normalized := Trim(Item);
    if (Length(Normalized) >= 2) and (Normalized[1] = '"') and (Normalized[Length(Normalized)] = '"') then
      Normalized := Copy(Normalized, 2, Length(Normalized) - 2);
    if CompareText(RemoveBackslashUnlessRoot(Normalized), RemoveBackslashUnlessRoot(Entry)) <> 0 then begin
      if HasItem then Result := Result + ';';
      Result := Result + Item;
      HasItem := True;
    end;
  until Split = 0;
end;

procedure RemoveOwnedPath;
var Owned, Current: String;
begin
  if RegQueryStringValue(HKCU, OwnerKey, 'PathEntry', Owned) then begin
    if RegQueryStringValue(HKCU, 'Environment', 'Path', Current) then
      if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', WithoutEntry(Current, Owned)) then
        RaiseException('Could not update the user PATH.');
    RegDeleteValue(HKCU, OwnerKey, 'PathEntry');
    RegDeleteKeyIfEmpty(HKCU, OwnerKey);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Current, Directory, Owned: String;
begin
  if CurStep = ssPostInstall then begin
    Directory := ExpandConstant('{app}');
    if RegQueryStringValue(HKCU, OwnerKey, 'PathEntry', Owned) then
      if (CompareText(Owned, Directory) <> 0) or not WizardIsTaskSelected('addtopath') then
        RemoveOwnedPath;
    if WizardIsTaskSelected('addtopath') then begin
      RegQueryStringValue(HKCU, 'Environment', 'Path', Current);
      if WithoutEntry(Current, Directory) <> Current then exit;
      if Current <> '' then Current := Current + ';';
      if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Current + Directory) then
        RaiseException('Could not update the user PATH.');
      if not RegWriteStringValue(HKCU, OwnerKey, 'PathEntry', Directory) then
        RaiseException('Could not record the installed PATH entry.');
    end;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then RemoveOwnedPath;
end;
