# Changelog

Releasing: add an entry below, then tag `vX.Y.Z`. CI builds a binary for each
platform and attaches them to the GitHub release.

## 0.1.3

- The command line has its own repository. Until now it lived inside the
  skills repository (`livellm_cloud_skills/cli`, tags `cli-v0.1.0`–`cli-v0.1.2`);
  the code is the same.

## 0.1.2

- `create apps -f stack.json`: several apps at once, all or nothing.

## 0.1.1

- `ls` says why it couldn't read how things are running, instead of showing
  "unknown" for everything without a word.
- The README says why `login` asks for full access by default.

## 0.1.0

- `livellm`: sign in, list, status, logs, connect, keys, create, rm, restart,
  build, builds, deploy. One binary, no dependencies, five platforms.
