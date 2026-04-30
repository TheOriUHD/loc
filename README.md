# loc

`loc` counts code, comment, and blank lines in a directory tree. It is written in Go 1.22+, builds as a single static binary, and runs on Linux and macOS from anywhere on your `PATH`.

## Install

Build and install locally:

```sh
make install
```

The install target copies `loc` to `/usr/local/bin/loc` when that directory is writable, otherwise it falls back to `~/.local/bin/loc` and prints the path it used.

Install a release binary:

```sh
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
curl -L "https://github.com/theoriuhd/loc/releases/latest/download/loc-${os}-${arch}" -o /usr/local/bin/loc
chmod +x /usr/local/bin/loc
```

The release assets are named like `loc-darwin-arm64`, `loc-darwin-amd64`, `loc-linux-arm64`, and `loc-linux-amd64`.

## Usage

Run without arguments in a terminal to launch the interactive wizard, then open the results TUI:

```sh
loc
```

Run with a path in a terminal to scan directly and open the results TUI:

```sh
loc ~/projects/foo
```

Run in scriptable or legacy-output modes with flags:

```sh
loc ~/projects/foo --json
loc ~/projects/foo --legacy
loc ~/projects/foo --no-interactive
loc . --include Go,TypeScript
loc . --exclude Markdown,JSON --jobs 8
loc . --profile cpu
loc --version
```

Flags:

```text
loc [path]
  --include <lang1,lang2,...>  only include selected languages
  --exclude <lang1,lang2,...>  exclude selected languages
  --json                       print machine-readable output
  --legacy                     print the static table instead of opening the TUI
  --no-interactive             never prompt
  --jobs N                     parallel workers, default runtime.NumCPU() * 2
  --profile cpu|mem            write ./loc-cpu.pprof or ./loc-mem.pprof
  --version                    print version
  --help                       print help
```

Mode matrix:

```text
loc                         wizard -> scan -> TUI
loc <path>                  scan -> TUI
loc <path> --legacy         static table
loc <path> --json           JSON
loc <path> --no-interactive static table
loc with non-TTY stdin      static table
```

If any flag or positional path is given, `loc` skips the wizard. It also skips the wizard when stdin is not a TTY or when `--no-interactive` is set, which makes it suitable for CI or piped use.

## Results TUI

The default terminal UI scans first with a spinner and progress indicator, then opens a desktop-style results app with a menu bar, bordered header, scrollable sortable table, active-filter pane, and status bar.

```text
 File  Edit  View  Filters  Export  Help
╭────────────────────────────────────────────────────────────────────╮
│ 📁 ~/Documents/LineCounter                                         │
│ 2,360 files · 1,113,893 code · 94,486 comments · 105,831 blanks    │
╰────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────╮
│ Language              Files         Code     Comments     Blanks   │
│ ─────────────────────────────────────────────────────────────────  │
│ ❯ Go                     42        8,512          912        421   │
│   Python                 21        5,042          313        881   │
╰────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────╮
│ Filters: langs(3) · path(*) · size:>10KB · comments:<50%           │
╰────────────────────────────────────────────────────────────────────╯
↑↓: navigate  /: search  f: filters  e: export  q: quit
```

Menus open with Alt+their underlined trigger letter or mouse clicks. File and View actions support New Scan, Open Folder, sorting, reverse order, skipped-file details, and quit. Filters support language, glob path, minimum size, comment ratio, and minimum-line filters. Export supports saving or copying JSON, CSV, Markdown, HTML, and plain text.

## Interactive Wizard

When `loc` starts with no arguments in an interactive terminal, it opens a Spotlight-style path picker before the language filter prompts:

```text
┌─────────────────────────────────────────────────┐
│  Search: ~/Doc█                                  │
├─────────────────────────────────────────────────┤
│  ❯ ~/Documents                                   │
│    ~/Documents/LineCounter                       │
│    ~/Documents/Projects                          │
│    ~/Downloads                                   │
│    ~/Desktop                                     │
└─────────────────────────────────────────────────┘
 Tab: complete   ↑↓: navigate   Enter: select   Esc: cancel
```

The picker seeds results from `$HOME`, `$PWD`, immediate children of `$PWD`, and recent successful scan paths from `~/.config/loc/history`. It supports fuzzy matching, shell-style Tab completion, arrow-key navigation, and `~` abbreviation for the home directory.

## Build

```sh
make build
```

This writes the host binary to `./bin/loc` using:

```sh
go build -ldflags="-s -w -X main.version=<version>"
```

Cross-compile all release targets:

```sh
make release
```

This writes:

```text
dist/loc-linux-amd64
dist/loc-linux-arm64
dist/loc-darwin-amd64
dist/loc-darwin-arm64

## Releasing

Maintainers: `./scripts/release.sh X.Y.Z`

```

Remove installed binaries from `/usr/local/bin/loc` and `~/.local/bin/loc`:

```sh
make uninstall
```

## Counting Rules

`loc` maps files by extension and groups unknown extensions as `Other`. It tracks code, comments, blanks, total lines, file count, and skipped files. By default it skips common generated or dependency directories such as `node_modules`, `.git`, `dist`, `build`, `target`, `__pycache__`, virtualenv directories, `.next`, `out`, `.cache`, `.idea`, and `.vscode`.

Binary files are detected by reading the first 8KB and checking for null bytes or invalid UTF-8. Binary files, permission errors, and other unreadable files are counted as skipped and do not crash the scan.

## Performance

The counter streams file paths from `filepath.WalkDir` into buffered worker channels and does not follow symlinks by default. Files are opened once, binary detection peeks through a `bufio.Reader`, and line counting uses `ReadSlice('\n')` with manual long-line handling instead of `bufio.Scanner`.

Benchmark command used for the current numbers:

```sh
timeout 60 ./bin/loc /opt/homebrew/Cellar/go/1.26.2/libexec/src --no-interactive --json
```

Measured on April 30, 2026 on an Apple NVMe-class laptop, warm filesystem cache:

```text
Corpus:      Go source distribution, /opt/homebrew/Cellar/go/1.26.2/libexec/src
Files:       11,329 candidate files (10,633 counted, 696 binary skipped)
Bytes:       120.74 MiB after ignored-directory pruning
Wall time:   0.28s
Throughput:  40.5k files/sec, 431 MiB/sec
```

This local corpus did not reach the 50k files/sec target, but it confirms the scanner no longer hangs and that walking streams work promptly. With `LOC_DEBUG_TIMING=1`, the first file was dispatched in under 1ms and the walk completed in about 660ms on the same tree.

Profiling output can be captured with:

```sh
loc /path/to/repo --no-interactive --profile cpu
go tool pprof ./loc-cpu.pprof

loc /path/to/repo --no-interactive --profile mem
go tool pprof ./loc-mem.pprof
```
