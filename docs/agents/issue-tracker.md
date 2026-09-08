# Issue tracker: GitHub

Issues and PRDs belong to `EdVulcan/ticket-system` on GitHub. Use `gh` from this repository and verify the target with `git remote -v`.

- Read: `gh issue view <number> --comments`.
- List: `gh issue list --state open --json number,title,body,labels`.
- Create when authorized: `gh issue create --title "..." --body-file <file>`.
- Comment when authorized: `gh issue comment <number> --body-file <file>`.
- Label when authorized: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`.
- Close when authorized: `gh issue close <number> --comment "..."`.

“Publish to the issue tracker” means create a GitHub issue. “Fetch the relevant ticket” means read its issue and comments. Ordinary diagnosis or implementation does not by itself authorize creating issues, commenting, changing labels, closing issues, or pushing code. Do not publish credentials, usable ticket codes, or customer personal information.

Use `apply_patch` for local body files; follow the Windows shell safety rules. No remote issue or label was created during setup.
