# Agent Rules

## Script Language

- Project automation scripts must be written in Python 3.
- Do not add new PowerShell scripts for general project automation.
- If an existing Windows-specific PowerShell script needs changes, prefer migrating it to a Python 3 equivalent or adding a Python 3 entrypoint.
- Keep generated scripts cross-platform where practical, with Windows/Linux differences handled inside Python code instead of separate shell scripts.

## Studio UI Style

- Use the shadcn dashboard example as the default visual baseline: https://ui.shadcn.com/examples/dashboard
- Prefer a light neutral admin console style: white background, subtle gray surfaces, thin borders, low or no shadows, compact spacing, and restrained color.
- Do not use dark industrial sidebars, heavy gradients, decorative grid backgrounds, oversized cards, or large marketing-style typography for the admin console.
- Keep typography dense and dashboard-like: body text around 13px, sidebar and table text around 12-13px, panel titles around 15px, and page titles around 22px.
- Navigation should feel like shadcn: compact left sidebar, simple grouped items, quiet hover state, clear active item, and icons used as small supporting marks rather than large decorations.
- Data-heavy pages should prioritize scanability: tables, compact metric cards, inline filters, small badges, and clear action buttons.
- Use Radix Themes and Tailwind CSS already present in the studio; avoid switching UI libraries unless explicitly requested.

## Code Commit Principles

- Create a git commit at each important checkpoint:
  - Data model and API/interface definitions are complete.
  - Core logic implementation is complete.
  - Tests are written and passing for the checkpoint scope.
  - Documentation updates are complete.
- Checkpoint commit messages must start with `[WIP]`.
- After the feature is complete, clean up the checkpoint history with `git commit --amend` or an interactive rebase before finalizing the branch.
- Do not commit unrelated local changes. Keep `.idea` and other local IDE metadata untracked.
