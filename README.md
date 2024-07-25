# Operator CLI for mev-commit AVS

This repo implements a CLI for Eigenlayer operators to register and/or deregister with the mev-commit AVS.

## Registration

To register an operator EOA account with the mev-commit AVS, the operator's relevant keystore must be accessible. To encrypt a private key to a keystore file, use the [mev-commit monorepo's keystore generator](https://github.com/primev/mev-commit/tree/main/infrastructure/tools/keystore-generator).

Then to register:

```bash
NAME:
   mev-commit-operator-cli register - Register an operator

USAGE:
   mev-commit-operator-cli register [command options]

OPTIONS:
   --operator-config value    Path to operator.yml config file [$OPERATOR_CONFIG]
   --avs-address value        Address of the mev-commit AVS contract [$AVS_ADDRESS]
   --boost-gas-params value   Whether to boost gas params to speed up tx inclusion [$BOOST_GAS_PARAMS]
   --keystore-password value  Password for the keystore [$KEYSTORE_PASSWORD]
   --log-level value          Log level, options are 'debug', 'info', 'warn', 'error' (default: "info") [$LOG_LEVEL]
   --log-fmt value            Log format, options are 'text' or 'json' (default: "text") [$LOG_FMT]
   --log-tags value           Log tags is a comma-separated list of <name:value> pairs that will be inserted into each log line [$LOG_TAGS]
   --help, -h                 show help
```

The first three command options are required. Your `operator.yml` will need to be accessible to perform this registration. This file is created as part of [registering as an operator with the EigenLayer CLI](https://docs.eigenlayer.xyz/eigenlayer/operator-guides/operator-installation), and does not need to be modified. See [Eigenlayer reference example](https://github.com/Layr-Labs/eigenlayer-cli/blob/master/pkg/operator/config/operator-config-example.yaml).


The keystore password can be provided as an option, otherwise the CLI will prompt for it.

The registration command will query data from the AVS contracts and sign over a hash of the following:

1. Operator address
2. Mev-commit AVS address
3. Unique salt
4. 1 hour expiry

Then a registration transaction is sent on behalf of the operator account with the signed hash to be validated on-chain.

## Deregistration

To deregister an operator from the mev-commit AVS, the operator account must first request deregistration:

```bash
USAGE:
   mev-commit-operator-cli request-deregistration [command options]
```

Then after waiting for the deregistration period to pass, the operator can deregister:

```bash
USAGE:
   mev-commit-operator-cli deregister [command options]
```

Both these commands use the same options as the registration command.

## Testing the cli

An example keystore file is committed to the `test/keystore` directory using the default key-pair: 

Account: `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266`
Private Key: `0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80`

To recreate, use the [mev-commit monorepo's keystore generator](https://github.com/primev/mev-commit/tree/main/infrastructure/tools/keystore-generator).

```bash
go run cmd/main.go import -private-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 --passphrase primev
```
