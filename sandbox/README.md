# goharness sandbox

Runs the goharness coding agent inside a Docker container that sees **only one project folder**. The agent can edit that folder and nothing else on the machine: no home directory, no SSH keys, no Docker socket, no other projects. By default it can also reach **only your model server**, not the rest of the network or the internet.

The model itself runs elsewhere (Olla, llama-swap or any OpenAI-compatible server). The sandbox only needs to reach it.

```
 your terminal ──docker run -it──▶ [ sandbox container ]  ──▶ relay ──▶ model server (Olla / llama-swap)
                                    sees /work only          (only route out)
                                    /work = your project
```

## What you need on the server

| Need | Notes |
|---|---|
| Linux x86-64 with Docker | Any recent Docker Engine (tested on 29.x). |
| Your user in the `docker` group | No root is needed to run it. |
| A model server reachable from the host | Any OpenAI-compatible `/v1` endpoint that serves the model named in the profile. |
| Disk | About 700 MB for the images. |

## Deploy on another server

### Easiest: one command from a machine that already has it

```bash
sandbox/deploy.sh you@newserver --base-url http://<model-server>:<port>/v1 --model <model-name>
```

That does everything over ssh: it checks Docker on the target, copies the launcher and README, sends the two images **only if the target doesn't already have the same ones** (so updates take seconds), keeps any profile you've already edited there, sets `BASE_URL` and `MODEL` if you pass them, and finishes with a check that the sandbox starts and reaches your model server. Running it again later updates the launcher and leaves your profiles alone.

Options: `--dir DIR` (install location on the target, default `~/goharness-container`), `--force-images` (send the images even if they match).

Needs ssh access from the machine you run it on to the target (set up a key once with `ssh-copy-id you@newserver`), and Docker on the target with your user in the `docker` group. Then on the target: `~/goharness-container/goharness-run <project-dir>`.

### No ssh between the machines: copy the bundle

On the machine where it already works:

```bash
sandbox/package.sh            # writes ~/goharness-sandbox-bundle-<date>.tar.gz (about 135 MB) and prints its sha256
scp ~/goharness-sandbox-bundle-*.tar.gz you@newserver:~
```

On the new server:

```bash
tar xzf goharness-sandbox-bundle-*.tar.gz            # creates ./goharness-container
cd goharness-container
docker load < images.tar.gz                           # loads goharness-sandbox:dev and gh-proxy:dev
nano profiles/default.conf                            # set BASE_URL and MODEL for THIS server (see below)
./goharness-run --exec 'echo sandbox ok' ~/some-project
```

The folder can live anywhere. The launcher finds its own location, and keeps its state under `./state/`.

### Build the image on the target instead

Needs a Linux x86-64 `goharness` binary, an `rtk` binary and a Node 22 folder on that server:

```bash
GOHARNESS_BIN=/path/to/goharness RTK_BIN=/path/to/rtk NODE_DIR=/path/to/node-v22 ./build-image.sh
```

The image is Ubuntu 24.04 with goharness, rtk, git, ripgrep and Node 22. The goharness binary links glibc, so build it on, or for, a glibc system. `build-image.sh` also builds the small relay image.

## Configure it for the new server

Edit `profiles/default.conf` (or make a copy per use). The settings that usually need changing:

| Setting | Meaning | Default |
|---|---|---|
| `BASE_URL` | The OpenAI-compatible endpoint the agent talks to. **Must be an address the host can reach.** | `http://172.16.10.46:40115/v1` |
| `MODEL` | A model that endpoint serves | `qwen36_35B_reap48_v2` |
| `CONTEXT` | The model's context window, in tokens | `49152` |
| `SUBAGENT_MODEL` | Model for delegated subagents. Use one the endpoint can serve on every host in the pool. | `gemma4_12B_qat` |
| `ROUNDS` | Tool-calling rounds per message | `30` |
| `PARALLEL` | Subagents allowed at the same time | `2` |
| `NETWORK` | `olla-only` (default) or `open` | `olla-only` |
| `MEMORY` | Memory cap for the container | `4g` |
| `SKILLS_DIR` | A host folder of skills, mounted read-only inside the sandbox | `~/.goharness/skills` |
| `PERMISSIONS_FILE` | A `permissions.json` copied into the agent's config | none |
| `RULES_FILES` | Project files turned into a `project-rules` skill the agent loads first | none |

You can also override per run: `GOHARNESS_MODEL=... GOHARNESS_BASE_URL=... ./goharness-run <project>`.

To list the models your endpoint serves: `curl <BASE_URL>/models`.

## Run it

```bash
./goharness-run <project-dir>                          # open the goharness terminal UI in the sandbox
./goharness-run --profile reviewer <project-dir>       # use profiles/reviewer.conf
./goharness-run --open-net <project-dir>               # normal networking (for npm install, git clone, web search)
./goharness-run --publish 8180:8080 <project-dir>        # reach a server the agent starts on port 8080 at http://<host>:8180 (implies normal networking, so the Olla-only lock is off; the app must listen on 0.0.0.0)
./goharness-run --exec 'ls -la; node -v' <project-dir> # run one command in the sandbox instead of goharness
```

Inside the UI, the project is at `/work`. Files the agent writes there are owned by **you** on the host. Exit with Ctrl+C (goharness shows its own key help). The container is removed on exit; sessions and settings stay in `state/gh-<project-name>/`.

**Use a copy or a git repo for the project.** The agent can change everything in the mounted folder. A git baseline (`git init && git add -A && git commit -m base`) makes every change visible with `git diff` and reversible with `git checkout . && git clean -fd`.

The bundled profiles:

- `default`: the settings above.
- `reviewer`: a smaller model, 20 rounds, parallel subagents, and a shared skills folder.
- `sparky-show`: loads the project's `CLAUDE.md` and `Context.md` as a skill, because goharness does not read them on its own.

## Skills in the sandbox

The agent loads skills from three places:

- **Shared skills:** the profile's `SKILLS_DIR` is mounted read-only at `/home/agent/.goharness/skills/shared`. The default is the host's `~/.goharness/skills`; a profile can point it at another folder.
- **`project-rules`:** if the profile sets `RULES_FILES`, the launcher builds this skill from those files each launch.
- **The project's own skills:** a `.goharness/skills` folder inside the project.

Skill folders outside these are not visible. A skill that calls a host-only path (for example a tool in a host venv) will not work in the sandbox; use a profile with its own `SKILLS_DIR`. The goharness inside the image is a copy built into it, so changes to goharness itself (such as the skills-folder rule) reach the sandbox only after the image is rebuilt.

## How the sandbox is limited

| What the agent can do | Allowed? |
|---|---|
| Read and write the mounted project | Yes |
| See the host home folder, `~/.ssh`, other projects | No |
| Use the Docker socket | No (not mounted) |
| Write outside `/work` and its own state folder | No (it runs as a non-root user, so system folders such as `/etc` are not writable) |
| Gain privileges | No (`--cap-drop ALL`, `no-new-privileges`) |
| Use more than `MEMORY` | No |
| Reach the model server | Yes |
| Reach the rest of the network or the internet | **No** in `olla-only` mode, yes with `--open-net` |

Links inside the project that point outside it (a `node_modules` link, for example) are mounted **read-only** at the same path, so the project's tools resolve them.

### How the network limit works (no root needed)

In `olla-only` mode the sandbox joins a Docker network created with `--internal`, which has no route out. A small relay container (`gh-relay`, running `socat`) is attached to that network and to the normal one, and forwards one port to your model server. The sandbox's `BASE_URL` is rewritten to point at the relay. Nothing else is reachable.

Because of this, **web search, `npm install` and `git clone` fail in `olla-only` mode.** Use `--open-net` when you need them.

## Verify it (about a minute)

```bash
cd goharness-container
P=~/some-project
./goharness-run --exec 'id -u; ls /home 2>&1 | head -1; touch /etc/x 2>&1' $P          # your uid; no host home; permission denied
./goharness-run --exec 'curl -s -m 5 -o /dev/null -w "model server: %{http_code}\n" http://gh-relay:<port>/v1/models' $P   # 200 (use your BASE_URL port)
./goharness-run --exec 'curl -s -m 4 -o /dev/null -w "internet: %{http_code}\n" https://example.com' $P   # 000 = blocked
./goharness-run --open-net --exec 'curl -s -m 4 -o /dev/null -w "internet: %{http_code}\n" https://example.com' $P   # 200 = control
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `permission denied` talking to Docker | Your user isn't in the `docker` group. |
| `Unable to find image 'goharness-sandbox:dev'` | The images weren't loaded: run `docker load < images.tar.gz`. |
| The UI opens but the model never answers | `BASE_URL` or `MODEL` is wrong for this server. Check `curl <BASE_URL>/models` from the host. |
| `no profile .../profiles/NAME.conf` | The profile file doesn't exist. |
| Blank or garbled screen | Run from a real terminal (not a pipe), and check `echo $TERM`. |
| Files owned by root in the project | You ran Docker by hand instead of through `goharness-run`, which sets your uid. |
| `tsc` or `npm` can't find modules | The project's `node_modules` is a real folder? Then it's mounted already. If it's a link, the target must exist on the host. |
| Model server is on `localhost` of the host | `localhost` inside the container is the container. Use the host's LAN address in `BASE_URL`. |

## Clean up

```bash
docker rm -f gh-relay                         # the relay (recreated automatically next time)
docker network rm gh-net                      # the internal network
docker rmi goharness-sandbox:dev gh-proxy:dev # the images
rm -rf <the sandbox folder on that machine>                  # the launcher, profiles and all saved sessions
```

## Not included yet

- A web UI mode exists (`--web [port]`) but is not network-limited, because Docker cannot publish ports from an internal network.
- goharness has no native profile support; the launcher applies profiles by setting flags, environment and mounts.
