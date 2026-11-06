#define MyAppName "Belgindo Office Messenger"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "PT. Belgindo Raya"
#define MyAppExeName "BelgindoOfficeMessenger.exe"

[Setup]
AppId={{A4B5C6D7-1E2F-4A5B-8C9D-0E1F2A3B4C5D}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL=https://belgindoraya.co.id
DefaultDirName={autopf}\Belgindo Office Messenger
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputDir=dist
OutputBaseFilename=BelgindoOfficeMessenger_Setup_v{#MyAppVersion}
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName}
Compression=lzma
SolidCompression=yes
WizardStyle=modern
LicenseFile=EULA.txt
; Nama mutex persis seperti yang dibuat aplikasi: Local\BelgindoMessengerSingle
; (satu per sesi login). Installer jadi tahu kalau aplikasi masih jalan.
AppMutex=Local\BelgindoMessengerSingle
; Sebelum menimpa / menghapus file: deteksi aplikasi yang masih jalan dan
; tawarkan menutupnya (mencegah error "file in use").
CloseApplications=yes
CloseApplicationsFilter=*.exe
RestartApplications=no
; Metadata berkas setup.exe untuk detail properti / SmartScreen.
VersionInfoVersion=1.0.0
VersionInfoCompany={#MyAppPublisher}
VersionInfoProductVersion={#MyAppVersion}
VersionInfoProductName={#MyAppName}
VersionInfoDescription={#MyAppName} Setup

[Languages]
; Bahasa wizard HARUS dinyatakan (sebelumnya tidak). Berkas Indonesian.isl tidak
; ikut terpasang pada Inno Setup 5/6/7, jadi wizard memakai English yang selalu
; ada; seluruh teks aplikasi sendiri mengikuti bahasa terpilih di menu Bantuan.
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Buat shortcut di Desktop"; GroupDescription: "Shortcut tambahan:"; Flags: unchecked

[Files]
Source: "dist\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "assets\*"; DestDir: "{app}\assets"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

; Entri "jalankan otomatis" dibuat hanya bila pengguna memcentangnya di
; Pengaturan; installer tidak pernah membuatnya diam-diam, tapi uninstall
; membersihkannya supaya tidak ada nilai yatim di key Run.
[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: none; ValueName: "BelgindoMessenger"; Flags: dontcreatekey uninsdeletevalue

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "Buka {#MyAppName}"; Flags: nowait postinstall skipifsilent
