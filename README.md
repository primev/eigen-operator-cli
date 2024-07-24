# eigen-operator-cli

## Test

An example keystore file is committed to the `test/keystore` directory using the default key-pair: 

Account: `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266`
Private Key: `0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80`

To recreate, use the [mev-commit monorepo's keystore generator](https://github.com/primev/mev-commit/tree/main/infrastructure/tools/keystore-generator).

```bash
go run cmd/main.go import -private-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 --passphrase primev
```
