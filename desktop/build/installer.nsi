; ResearchAssistant per-user installer.
; Build with: makensis /DVERSION=0.9.0 installer.nsi after staging
; ResearchAssistant.exe (from the MyGo build), backend.exe, sandbox.exe and
; optionally uv.exe.
Unicode true
!ifndef VERSION
  !define VERSION "0.9.0"
!endif
Name "ResearchAssistant"
OutFile "ResearchAssistant-Setup.exe"
InstallDir "$LOCALAPPDATA\Programs\ResearchAssistant"
InstallDirRegKey HKCU "Software\ResearchAssistant" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma

!include "MUI2.nsh"

!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "ResearchAssistant 安装"
!define MUI_WELCOMEPAGE_TEXT "本向导将安装 ResearchAssistant 桌面客户端。可以选择安装位置；用户数据会保存在 %LOCALAPPDATA%\ResearchAssistant，卸载时默认保留。"
!define MUI_FINISHPAGE_RUN "$INSTDIR\ResearchAssistant.exe"
!define MUI_FINISHPAGE_RUN_TEXT "运行 ResearchAssistant"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "ResearchAssistant"
VIAddVersionKey "CompanyName" "DeepSeek"
VIAddVersionKey "FileDescription" "ResearchAssistant desktop"
VIAddVersionKey "LegalCopyright" "Copyright DeepSeek"
VIAddVersionKey "FileVersion" "${VERSION}"

!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ResearchAssistant"

Section "Install"
  SetOutPath "$INSTDIR"
  File "ResearchAssistant.exe"
  File "backend.exe"
  File "sandbox.exe"
  File /nonfatal "uv.exe"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\ResearchAssistant.lnk" "$INSTDIR\ResearchAssistant.exe"
  CreateShortcut "$DESKTOP\ResearchAssistant.lnk" "$INSTDIR\ResearchAssistant.exe"

  WriteRegStr HKCU "Software\ResearchAssistant" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "ResearchAssistant"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1

  ; researchassistant:// protocol handler for browser round-trips.
  WriteRegStr HKCU "Software\Classes\researchassistant" "" "URL:ResearchAssistant Protocol"
  WriteRegStr HKCU "Software\Classes\researchassistant" "URL Protocol" ""
  WriteRegStr HKCU "Software\Classes\researchassistant\shell\open\command" "" '"$INSTDIR\ResearchAssistant.exe" "%1"'
SectionEnd

Section /o "Associate PDF"
  WriteRegStr HKCU "Software\Classes\.pdf\OpenWithProgids" "ResearchAssistant.pdf" ""
  WriteRegStr HKCU "Software\Classes\ResearchAssistant.pdf" "" "PDF Document"
  WriteRegStr HKCU "Software\Classes\ResearchAssistant.pdf\DefaultIcon" "" '"$INSTDIR\ResearchAssistant.exe,0"'
  WriteRegStr HKCU "Software\Classes\ResearchAssistant.pdf\shell\open\command" "" '"$INSTDIR\ResearchAssistant.exe" "%1"'
SectionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\ResearchAssistant.lnk"
  Delete "$DESKTOP\ResearchAssistant.lnk"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\Classes\researchassistant"
  DeleteRegKey HKCU "Software\Classes\ResearchAssistant.pdf"
  DeleteRegValue HKCU "Software\Classes\.pdf\OpenWithProgids" "ResearchAssistant.pdf"
  DeleteRegKey HKCU "Software\ResearchAssistant"
  RMDir /r "$INSTDIR"

  ; User data is kept by default. Silent uninstall never deletes it; an
  ; interactive uninstall asks once, explicitly.
  IfSilent +4
  MessageBox MB_YESNO|MB_ICONQUESTION "是否同时删除用户数据（计划、上传、运行时缓存）？选择“否”将保留数据以便重新安装。" IDNO +3
  RMDir /r "$LOCALAPPDATA\ResearchAssistant"
SectionEnd