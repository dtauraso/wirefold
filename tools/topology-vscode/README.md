# Topology Editor (VS Code extension)

Editor for the `topology/` directory tree. Drag nodes around; saves write back
through `WorkspaceEdit` and the runtime loader picks up the new spec on next run.

It is a **command-activated webview panel**, not a custom editor bound to a file
type — opening a `.json` file will not launch it.

---

# Setup from a brand-new Mac

If you already have Go, Node, VS Code and a clone of the repo, skip to
[Build](#build). Otherwise start here — no prior setup assumed.

Everything below is typed into **Terminal**. To open it: press `Cmd`+`Space`,
type `Terminal`, press `Return`. You get a window where you type a command and
press `Return` to run it. Commands are shown one per line; run them in order.

Some commands ask for your Mac login password. Nothing appears on screen as you
type it — that is normal, not a frozen terminal. Type it and press `Return`.

## 1. Apple's developer tools

This gives you `git`, which downloads the project.

```sh
xcode-select --install
```

A dialog appears — click **Install** and wait for it to finish (a few minutes).
If it instead says `command line tools are already installed`, you already have
them; move on.

## 2. Homebrew

Homebrew installs the other three tools. Paste this whole line:

```sh
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

When it finishes it prints a short **Next steps** section telling you to run two
more commands (an `echo` and an `eval`). **Run them.** On Apple Silicon Macs,
Homebrew is not usable until you do. Then confirm it worked:

```sh
brew --version
```

If that prints a version number, continue. If it says `command not found`, the
Next steps commands were not run — scroll up in the terminal and run them.

## 3. Go, Node, and VS Code

```sh
brew install go
brew install node
brew install --cask visual-studio-code
```

Go is needed both to build and to *run* the editor: the extension shells out to
the Go compiler every time you open it.

Now open VS Code once (`Cmd`+`Space`, type `Visual Studio Code`, `Return`). Inside
it press `Cmd`+`Shift`+`P`, type `shell command`, and choose
**Shell Command: Install 'code' command in PATH**. This makes `code` work in
Terminal.

## 4. Download the project

```sh
cd ~/Documents
git clone https://github.com/dtauraso/wirefold.git
cd wirefold
```

You are now inside the project folder. Later terminal sessions start back at your
home folder, so run `cd ~/Documents/wirefold` again to return here.

## 5. Check everything landed

```sh
go version
node -v
code -v
```

Three version numbers means you are ready. Any `command not found` means that
tool's step above did not complete.

---

# Build

From the repo root (`~/Documents/wirefold`):

```sh
cd tools/topology-vscode
npm install
npm run build
```

`npm install` takes a minute or two and prints a lot of text; that is normal.

The build step regenerates node definitions using the Go toolchain, so it fails
with `go: command not found` if Go is missing — that error means the prerequisite
is absent, not that the build is broken.

## Run it

```sh
code .
```

That opens this folder in VS Code. Press **F5**. A second VS Code window opens
(the Extension Development Host). In that new window:

1. **File → Open Folder** and choose the repo root — the folder containing
   `go.mod` and `topology/` (`~/Documents/wirefold`). The extension resolves
   everything relative to the first workspace folder, so opening a subfolder
   makes it fail to find the topology tree.
2. In the file list on the left, right-click the `topology` folder →
   **Topology: Open Editor**. (Or `Cmd`+`Shift`+`P` → **Topology: Open Editor**,
   which resolves `topology/` from the workspace root.)

The panel compiles the Go binary on open and starts streaming. First open is
slower because it compiles from scratch.

## Rebuilding while you work

`npm run watch` rebuilds the webview bundle on save. What you do after a rebuild
depends on which side changed:

- **webview code** (`src/webview/`) — close and reopen the panel.
- **extension-host code** (everything else in `src/`) — `Cmd`+`Shift`+`P` →
  **Developer: Reload Window** in the Extension Development Host.

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for the file map (extension side,
webview side, message protocol, spec-vs-viewer split).

## Packaging a .vsix

To install into your normal VS Code instead of running the dev host. The
`package` script calls `vsce`, which is not in `devDependencies`, so run it
through `npx`:

```sh
npx --yes @vscode/vsce package   # → topology-vscode-0.0.1.vsix
code --install-extension topology-vscode-0.0.1.vsix
```

The installed extension still needs Go on your PATH and the repo open as the
workspace folder.
