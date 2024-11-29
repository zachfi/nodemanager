.PHONY: drone
drone:
	@drone jsonnet --stream --format
	@drone lint --trusted

drone-signature:
ifndef DRONE_TOKEN
	$(error DRONE_TOKEN is not set, visit https://drone.zach.fi/account)
endif
	@DRONE_SERVER=https://drone.zach.fi drone sign --save zachfi/nodemanager .drone.yml
