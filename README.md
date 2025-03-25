# 📦 caladan

> My blog post: [Installing NPM Packages Very Quickly](https://healeycodes.com/installing-npm-packages-very-quickly)

<br>

Very experimental package manager. Mostly for learning about the performance of installing npm packages.

For now, it installs npm packages from a valid `package-lock.json`, and can run bin scripts.

```text
Usage:
  caladan install-lockfile <directory>
  caladan run <directory> <script> <args>
```

To install from a lockfile:

```bash
./build.sh
./caladan install-lockfile fixtures/1
```

Then, to run a script:

```bash
./caladan run fixtures/1 next info
```

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
