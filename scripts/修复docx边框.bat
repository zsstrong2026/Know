@echo off
chcp 65001 >nul
setlocal

rem Drag a broken .docx onto this file to repair the illegal table borders
rem (a:lnL / a:lnR / a:lnT / a:lnB) that make Word report "file is corrupted".

set "PY=python"
"%PY%" -c "import sys" >nul 2>nul || set "PY=py"

if "%~1"=="" (
    echo Drop .docx files onto this .bat to repair them.
    pause
    exit /b 1
)

"%PY%" "%~dp0docx_borders.py" %* -i
echo.
echo Done. The repaired file can now be opened in Word.
pause
