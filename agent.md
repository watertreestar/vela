# Agent Rules

## Script Language

- Project automation scripts must be written in Python 3.
- Do not add new PowerShell scripts for general project automation.
- If an existing Windows-specific PowerShell script needs changes, prefer migrating it to a Python 3 equivalent or adding a Python 3 entrypoint.
- Keep generated scripts cross-platform where practical, with Windows/Linux differences handled inside Python code instead of separate shell scripts.
