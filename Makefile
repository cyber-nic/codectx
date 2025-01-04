build:
	$(MAKE) build-client
	$(MAKE) build-server

client:
	$(MAKE) build-client
	apps/client/bin/client
	# cd apps/client && air

server:
	$(MAKE) build-server
	apps/server/bin/server
	# cd apps/server && air

build-client:
	mkdir -p apps/client/bin
	cd apps/client && go build -o ./bin/client

build-server:
	mkdir -p apps/server/bin
	cd apps/server && go build -o ./bin/server