.PHONY: drone
drone:
	@drone jsonnet --stream --format
	@drone lint
