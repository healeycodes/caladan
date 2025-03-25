#!/bin/bash

# comparing caladan install-lockfile vs bun install
# caches are cleared before each run
hyperfine \
  --warmup 2 \
  --runs 5 \
  --prepare 'cd fixtures/1 && bun pm cache rm && rm -rf node_modules && rm -rf false' \
  './caladan install-lockfile fixtures/1' \
  'cd fixtures/1 && bun install --force --ignore-scripts --no-cache --network-concurrency 64' \
