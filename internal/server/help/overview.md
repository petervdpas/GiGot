# Overview

> *Stub — fill me in.*

GiGot is a Git-backed server tailored for Formidable record stores. It
turns a small team's collection of Formidable templates, records, and
images into a versioned repository they can clone, sync, and mirror
without each member running their own Git host.

## What it does

- Stores Formidable contexts as plain Git repositories
- Issues per-user **subscription keys** scoped to one repo each
- Mirrors changes to optional remote destinations (GitHub, Azure DevOps)
- Provides a small admin console for repos, keys, credentials, accounts

## Who it's for

Small teams (≤ 15 people) who want shared Formidable records without
asking everyone to run their own Git server. One operator runs GiGot;
everyone else uses it via Formidable.
