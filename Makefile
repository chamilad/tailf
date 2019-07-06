GOOS=linux
GOARCH=amd64

BINARY=tailf-${GOOS}-${GOARCH}
TARGET=target
FATBINARY=${TARGET}/${BINARY}
CBINARY=${TARGET}/${BINARY}-compressed

# todo: version releases

.DEFAULT_GOAL: $(BINARY)

$(BINARY): clean 
	mkdir -p ${TARGET}
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -a -o ${FATBINARY}
	
release: clean
	mkdir -p ${TARGET}
	env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags="-s -w" -a -o ${FATBINARY}
	@upx --ultra-brute --best -v -o ${CBINARY} ${FATBINARY} 

clean: 
	go clean
	rm -rf ${TARGET}

