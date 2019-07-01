BINARY=tailf

.DEFAULT_GOAL: $(BINARY)

$(BINARY): clean 
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -a -o ${BINARY}
	upx --brute ${BINARY}

clean: 
	go clean
	rm -rf ${BINARY}

