<img src="assets/icon.svg" width="64" height="64" alt="">

# Fylvo

Your files. Your cloud.

Fylvo is a private cloud based on [Cloudreve](https://github.com/cloudreve/cloudreve) (GPL-3.0).
See [NOTICE](NOTICE) for attribution and [LICENSE](LICENSE) for the license.

## Layout

| Path | What it is | Comes from |
| --- | --- | --- |
| `/` (Go code) | Server | `cloudreve/cloudreve`, remote `upstream` |
| `frontend/` | Web app | `cloudreve/frontend`, remote `upstream-frontend` |
| `desktop/` | Windows sync client | `cloudreve/desktop`, remote `upstream-desktop` |
| `scripts/` | Build and deploy scripts | Fylvo |

## Build and deploy

Needs Go, Node.js (with corepack) and Python or `zip` on the build machine.

```bash
scripts/build.sh                                  # builds release/cloudreve for linux/amd64
scripts/deploy-nas.sh /srv/fylvo-test # builds the image on the NAS and restarts the stack
```

`deploy-nas.sh` reads the sudo password of the NAS user from stdin.

## Updates from Cloudreve

```bash
git fetch upstream && git merge upstream/master
git subtree pull --prefix=frontend upstream-frontend master --squash
git subtree pull --prefix=desktop upstream-desktop main --squash
```
