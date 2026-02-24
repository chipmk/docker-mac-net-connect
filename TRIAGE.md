# Issue & PR Triage - docker-mac-net-connect

> Snapshot taken 2026-02-12. 24 open issues, 12 open PRs.

## Project Context

docker-mac-net-connect creates a WireGuard tunnel between macOS and the Docker Desktop Linux VM so you can reach containers by IP without port binding. It runs as a Homebrew service. The codebase is small (~650 lines of Go across 3 files) but has accumulated a backlog of issues and PRs spanning 4+ years.

---

## Theme 1: Docker Desktop Compatibility (Critical)

Every major Docker Desktop update risks breaking the tool. This is the #1 recurring pain point.

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#62](https://github.com/chipmk/docker-mac-net-connect/issues/62) | Issue | Docker Desktop 4.52.0 broke things | API version 1.41 too old, min is now 1.44 |
| [#61](https://github.com/chipmk/docker-mac-net-connect/pulls/61) | PR | Updated Docker API and other modernisations | **Fixes #62** - bumps Docker client SDK |
| [#46](https://github.com/chipmk/docker-mac-net-connect/issues/46) | Issue | Docker Desktop 4.37.2 breaking things | Same class of problem, older version |
| [#41](https://github.com/chipmk/docker-mac-net-connect/issues/41) | Issue | Docker Desktop host networking conflicts | DD 4.34.0 host networking feature causes login failures |
| [#36](https://github.com/chipmk/docker-mac-net-connect/issues/36) | Issue | Resource Saver kills the tunnel | DD's idle VM shutdown (default since 4.24) breaks WireGuard |
| [#21](https://github.com/chipmk/docker-mac-net-connect/issues/21) | Issue | Stopped working with DD 4.16.1 | Old - likely resolved by now |
| [#20](https://github.com/chipmk/docker-mac-net-connect/issues/20) | Issue | Docker crashes | Old report, DD version unknown |

**How it ties together:** PR #61 directly solves #62 (and likely #46) by updating the Docker Go client to support newer API versions. The recent commit `8b0ad18` already bumped Go to 1.26 and updated server-side deps, but #61 goes further with the client SDK. Issues #36 and #41 are architectural - they need design changes (reconnect logic for Resource Saver, conflict detection for host networking), not just dep bumps.

**Suggested action:** Merge or supersede PR #61. Issues #21 and #20 are likely stale and could be closed.

---

## Theme 2: Dependency Updates & CI Hygiene

A wave of Dependabot PRs just landed alongside two tracking issues for modernizing the build.

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#67](https://github.com/chipmk/docker-mac-net-connect/issues/67) | Issue | Fix golangci-lint errors | Currently suppressed with `only-new-issues` |
| [#66](https://github.com/chipmk/docker-mac-net-connect/issues/66) | Issue | Bump client Go module to 1.26 | Client go.mod still targets Go 1.17 |
| [#73](https://github.com/chipmk/docker-mac-net-connect/pulls/73) | PR | Bump golangci-lint-action 7 -> 9 | Dependabot |
| [#72](https://github.com/chipmk/docker-mac-net-connect/pulls/72) | PR | Bump actions/setup-go 5 -> 6 | Dependabot |
| [#71](https://github.com/chipmk/docker-mac-net-connect/pulls/71) | PR | Bump go-iptables 0.6.0 -> 0.8.0 (client) | Dependabot |
| [#70](https://github.com/chipmk/docker-mac-net-connect/pulls/70) | PR | Bump actions/github-script 7 -> 8 | Dependabot |
| [#69](https://github.com/chipmk/docker-mac-net-connect/pulls/69) | PR | Bump netlink to 1.3.1 (client) | Dependabot |
| [#68](https://github.com/chipmk/docker-mac-net-connect/pulls/68) | PR | Bump actions/checkout 4 -> 6 | Dependabot |
| [#55](https://github.com/chipmk/docker-mac-net-connect/pulls/55) | PR | Update dependencies | Community PR from avoidik |
| [#35](https://github.com/chipmk/docker-mac-net-connect/pulls/35) | PR | Bump vulnerable dependencies | 2+ years old, likely superseded |

**How it ties together:** Issue #66 is the root cause - the client module's go.mod is stuck on Go 1.17 with 2021-era deps. Once #66 is resolved (bump client to Go 1.26), Dependabot PRs #71 and #69 become straightforward merges. The CI action PRs (#73, #72, #70, #68) are independent and can be merged anytime. Issue #67 (lint fixes) is blocked until the Go version bump lands since golangci-lint v2 won't run against Go 1.17 code. PR #35 is almost certainly superseded by the newer dep updates. PR #55 may also be superseded depending on overlap with #61 and the Dependabot PRs.

**Suggested action:** Resolve #66 first, then merge Dependabot PRs, then tackle #67. Close #35 and evaluate #55 for overlap.

---

## Theme 3: Alternative Runtime Support

Users want this tool to work beyond Docker Desktop for Mac.

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#26](https://github.com/chipmk/docker-mac-net-connect/issues/26) | Issue | Support lima environments | Tracking issue from gregnr |
| [#27](https://github.com/chipmk/docker-mac-net-connect/pulls/27) | PR | feat: Support Lima VMs iptables rules | DRAFT - adds Lima support, needs polish |
| [#16](https://github.com/chipmk/docker-mac-net-connect/issues/16) | Issue | Help making it work with colima | Community request |
| [#22](https://github.com/chipmk/docker-mac-net-connect/issues/22) | Issue | Minikube support? | Community request |
| [#13](https://github.com/chipmk/docker-mac-net-connect/issues/13) | Issue | Podman support | Community request |
| [#37](https://github.com/chipmk/docker-mac-net-connect/pulls/37) | PR | Support Windows (Docker Desktop) | Community PR, author unsure if it belongs here |

**How it ties together:** Issue #26 is the umbrella for Lima-based tools (colima, Rancher Desktop). Draft PR #27 is a partial implementation - it adds iptables rules for Lima VMs but still requires manual Docker socket symlinks. Issues #16 and #22 are effectively duplicates of #26 for specific Lima-based tools. #13 (Podman) and #37 (Windows) are separate platforms entirely. The config file feature (#24 in Theme 4) would help here - supporting custom Docker socket paths is a prerequisite for clean Lima/colima support.

**Suggested action:** #27 needs a maintainer to shepherd it to completion. #37 (Windows) is a large scope change - decide if it belongs in this repo or a fork. #13 and #22 could be consolidated under #26.

---

## Theme 4: Feature Requests

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#24](https://github.com/chipmk/docker-mac-net-connect/issues/24) | Issue | Config file | Filter networks, custom socket paths, etc. |
| [#25](https://github.com/chipmk/docker-mac-net-connect/pulls/25) | PR | Allow connections to internal networks | DRAFT - addresses #23 |
| [#23](https://github.com/chipmk/docker-mac-net-connect/issues/23) | Issue | Support --internal networks | Currently skipped |
| [#15](https://github.com/chipmk/docker-mac-net-connect/issues/15) | Issue | Filter specific networks | Want to limit which networks get routed |
| [#47](https://github.com/chipmk/docker-mac-net-connect/issues/47) | Issue | Support DD's built-in Kubernetes | Feature request |
| [#8](https://github.com/chipmk/docker-mac-net-connect/issues/8) | Issue | --net="host" support | Want host network mode to work |
| [#6](https://github.com/chipmk/docker-mac-net-connect/issues/6) | Issue | Bind multiple ports to one IP | Feature request |
| [#54](https://github.com/chipmk/docker-mac-net-connect/issues/54) | Issue | Add IPs to network diagram | Docs improvement |
| [#3](https://github.com/chipmk/docker-mac-net-connect/issues/3) | Issue | GitHub Actions | CI/CD tracking issue from 2021 |

**How it ties together:** Issues #15 and #23 both want network filtering - #24 (config file) is the enabler for both. Draft PR #25 partially addresses #23 by allowing internal networks. #47, #8, and #6 are standalone feature requests with no active work. #3 is likely partially resolved given CI exists now. #54 is a small docs fix.

**Suggested action:** #24 (config file) would unblock multiple issues and is worth prioritizing. PR #25 is close to addressing #23. #3 can probably be closed since CI workflows exist.

---

## Theme 5: Support / Troubleshooting (Low Priority)

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#30](https://github.com/chipmk/docker-mac-net-connect/issues/30) | Issue | Mac M2 can't connect | Likely resolved or needs more info |
| [#29](https://github.com/chipmk/docker-mac-net-connect/issues/29) | Issue | Question about root ownership | Brew service runs as root |
| [#28](https://github.com/chipmk/docker-mac-net-connect/issues/28) | Issue | Access containers from other machines | Out of scope - tool is for local access |
| [#12](https://github.com/chipmk/docker-mac-net-connect/issues/12) | Issue | How to uninstall? | Docs gap |

**How it ties together:** These are one-off support questions, mostly from 2022-2023. They indicate gaps in documentation (uninstall instructions, root ownership explanation) rather than code issues.

**Suggested action:** Answer and close. #28 is out of scope. #30 is likely stale. #12 and #29 could be addressed with README updates.

---

## Recommended Priority Order

1. **Merge dependency updates** - CI actions (#68, #70, #72, #73), then tackle #66 (client Go bump), then client deps (#69, #71)
2. **Fix Docker Desktop compat** - Evaluate PR #61 vs what's already landed, resolve #62
3. **Lint cleanup** - #67, once Go version is bumped
4. **Config file** - #24 unblocks network filtering (#15, #23) and custom sockets (#26)
5. **Internal networks** - Finish PR #25
6. **Lima support** - Finish PR #27, consolidate #16/#22 under #26
7. **Stale issue cleanup** - Close #21, #20, #3, #30, #28, #12 with appropriate responses

## PR Overlap / Superseded Work

| PR | Status | Likely superseded by |
|----|--------|---------------------|
| #35 (vuln deps) | Stale (2024) | Dependabot PRs + #61 |
| #55 (update deps) | Stale (2025) | Dependabot PRs + #61 |
| #61 (Docker API) | Active | Partially by commit `8b0ad18`, but still needed for client SDK |
