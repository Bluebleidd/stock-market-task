@echo off

if "%~1"=="" (
    echo Usage: start.bat ^<PORT^>
    exit /b 1
)

set APP_PORT=%~1

docker compose up --build