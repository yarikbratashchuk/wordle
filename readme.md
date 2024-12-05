# wordle

## Install and run

1. Install rollkit and ignite:


```bash
curl -sSL https://rollkit.dev/install.sh | sh -s v0.14.1
```

```bash
curl https://get.ignite.com/cli@v28.5.3! | bash
```

2. Init chain and rollkit config:

```bash
ignite chain build && ignite rollkit init
```

```bash
rollkit toml init
```

3. Run chain:

```bash
rollkit start --rollkit.aggregator --rollkit.sequencer_rollup_id wordle
```