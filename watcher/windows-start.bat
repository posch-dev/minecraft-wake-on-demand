@echo off
set MC_WOL_CONFIG=%~dp0..\config.yml
python "%~dp0mc_wol_proxy.py"
pause
