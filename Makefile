test-register:
	go run cmd/main.go register \
		--operator-config test/operator.yml \
		--avs-address 0x5FC8d32690cc91D4c39d9d3abcBD16989F875707 \
		--boost-gas-params true \
		--log-level debug
