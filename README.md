# 📦 caladan

> My blog post: [Installing NPM Packages Very Quickly](https://healeycodes.com/installing-npm-packages-very-quickly)

<br>

Very experimental package manager. Mostly for learning about the performance of installing npm packages.

For now, it installs npm packages from a valid `package.json`, or from a valid `package-lock.json`, and it can also run bin scripts.

```text
Usage:
  caladan install <directory>
  caladan install-lockfile <directory>
  caladan run <directory> <script> <args>
```

To install from `package-lock.json`:

```bash
./build.sh
./caladan install-lockfile fixtures/1
```

To install from `package.json`:

```bash
./build.sh
./caladan install fixtures/1
```

Then, to run a script:

```bash
./caladan run fixtures/1 next info
```

<br>

## Current issues

I can't find an npm-compatible semver library written in Go (or, written in something I can easily call from Go like C). So for now, I call `semver` in Node.js via stdin/stdout (and it's very slow!) 😭

This doesn't affect `install-lockfile` as we don't resolve versions (it works like "frozen lockfile").

<br>

## Tests

```bash
go test ./...
```

<br>

## Benchmark

Compare `caladan install-lockfile` to `bun install`.

Requires `hyperfine`.

```bash
./benchmark.sh
```

<br>

## Create CPU Profiles

Outputs two profiles (with and without the unzip semaphore).

```bash
./profile.sh
```

<br>

Named after the third planet orbiting the star Delta Pavonis.
