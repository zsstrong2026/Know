@echo off
REM Two-stage eval runner (v1.2 sect.8: contract probes fail-fast, then serial metric suites).
REM Usage: ci\run_eval.bat   (configure .env first, see .env.example)
setlocal
cd /d "%~dp0.."
set DEEPEVAL_PER_ATTEMPT_TIMEOUT_SECONDS_OVERRIDE=300

if not exist reports mkdir reports

echo [1/2] Contract probes (fail-fast)...
python -m pytest -m contract -x --tb=short
if errorlevel 1 (
    echo Contract probes FAILED. Aborting metric suites.
    exit /b 1
)

echo [2/2] Metric suites (qa + tools, serial)...
python -m pytest -m "not contract" --tb=short -v --junitxml=reports\junit_eval.xml
exit /b %errorlevel%
