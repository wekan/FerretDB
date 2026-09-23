@echo off
setlocal
where bash >nul 2>nul || (echo Git Bash is required.& exit /b 1)
if not "%~1"=="" goto cli
echo 1^) Release All
echo 2^) Release All Missing
echo 0^) Exit
set "choice="
set /p "choice=Selection: "
if "%choice%"=="0" exit /b 0
if "%choice%"=="1" (bash "%~dp0build.sh" release-all & exit /b)
if "%choice%"=="2" (bash "%~dp0build.sh" release-all-missing & exit /b)
echo Invalid selection
exit /b 2
:cli
bash "%~dp0build.sh" %*
exit /b %errorlevel%
