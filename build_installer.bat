@echo off
setlocal EnableDelayedExpansion
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
  echo ERROR: Go belum terpasang atau tidak ada di PATH.
  pause
  exit /b 1
)

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1
if not exist "dist" mkdir "dist"

echo [1/2] Membangun Belgindo Office Messenger...
go build -mod=vendor -trimpath -ldflags="-H=windowsgui" -o "dist\BelgindoOfficeMessenger.exe" .\cmd\messenger
if errorlevel 1 (
  echo ERROR: Build aplikasi gagal.
  pause
  exit /b 1
)

REM --- Cari ISCC.exe (Inno Setup 5 / 6 / 7) ---
set "ISCC="

where ISCC.exe >nul 2>nul
if not errorlevel 1 (
  set "ISCC=ISCC.exe"
  goto :found_iscc
)

REM Cek lokasi umum Inno Setup
for %%P in (
  "%ProgramFiles(x86)%\Inno Setup 7\ISCC.exe"
  "%ProgramFiles%\Inno Setup 7\ISCC.exe"
  "%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe"
  "%ProgramFiles%\Inno Setup 6\ISCC.exe"
  "%ProgramFiles(x86)%\Inno Setup 5\ISCC.exe"
  "%ProgramFiles%\Inno Setup 5\ISCC.exe"
  "%LocalAppData%\Programs\Inno Setup 7\ISCC.exe"
  "%LocalAppData%\Programs\Inno Setup 6\ISCC.exe"
) do (
  if exist "%%~P" (
    set "ISCC=%%~P"
    goto :found_iscc
  )
)

echo [2/2] ISCC Inno Setup tidak ditemukan.
echo EXE berhasil dibuat di dist\BelgindoOfficeMessenger.exe
echo.
echo Tips: Pastikan Inno Setup sudah terpasang, lalu jalankan ulang script ini.
echo Atau tambahkan folder Inno Setup ke PATH sistem.
pause
exit /b 0

:found_iscc
echo [2/2] Membuat installer Windows...
echo Menggunakan: !ISCC!
"!ISCC!" "%~dp0installer.iss"
if errorlevel 1 (
  echo.
  echo ERROR: Build installer gagal.
  echo Periksa pesan error di atas.
  pause
  exit /b 1
)

echo.
echo [3/3] Menghitung SHA256 installer...
REM Hash dipakai penerima installer untuk mencocokkan berkas (antisipasi.txt:
REM exe tanpa tanda tangan sering dicurigai SmartScreen/antivirus).
set "SHAFILE=dist\BelgindoOfficeMessenger_Setup_v1.0.0.sha256.txt"
certutil -hashfile "dist\BelgindoOfficeMessenger_Setup_v1.0.0.exe" SHA256 | findstr /v ":" > "%SHAFILE%"
if errorlevel 1 (
  echo WARNING: certutil gagal menghitung hash.
) else (
  echo Hash tersimpan di %SHAFILE%
)

REM --- Opsional: tanda tangan kode (mengurangi peringatan SmartScreen) ---
REM Hapus remark di bawah bila punya sertifikat Authenticode.
REM signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 /a "dist\BelgindoOfficeMessenger_Setup_v1.0.0.exe"
REM signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 /a "dist\BelgindoOfficeMessenger.exe"

echo.
echo Selesai. Installer tersedia di folder dist:
echo   dist\BelgindoOfficeMessenger_Setup_v1.0.0.exe
pause
