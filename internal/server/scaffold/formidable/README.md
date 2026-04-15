# Formidable context

This repository is managed by [GiGot](https://github.com/petervdpas/GiGot) and
laid out as a [Formidable](https://github.com/petervdpas/Formidable) context.

- `templates/` — YAML template definitions. A starter `basic.yaml` is
  included; add or edit templates here.
- `storage/` — per-template instance data. Each template gets its own
  subdirectory containing `*.meta.json` forms and an `images/` directory.
  The included `.gitkeep` keeps the folder in version control until the
  first form is saved.

Clone this repo from a Formidable client and point the app's context folder
at the working copy. Changes pushed back to GiGot are distributed to every
other enrolled client on their next pull.
