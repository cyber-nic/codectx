build:
	mkdir -p apps/client/bin
	cd apps/client && go build -o ./bin/client

	mkdir -p apps/server/bin
	cd apps/server && go build -o ./bin/server

client:
	apps/client/bin/client

server:
	apps/server/bin/server
