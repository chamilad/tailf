GOOS=linux
GOARCH=amd64

VERSION="v0.2"
BUILD=`git rev-parse --short HEAD`

BINARY=tailf-${VERSION}-${GOOS}-${GOARCH}
TARGET=target
FATBINARY=${TARGET}/${BINARY}
CBINARY=${TARGET}/${BINARY}-compressed

# pass version and build into source, compress when building
LDFLAGS=-ldflags "-extldflags '-static' -X main.Version=${VERSION} -X main.Build=${BUILD} -s -w"

.DEFAULT_GOAL: $(BINARY)

$(BINARY): clean 
	mkdir -p ${TARGET}
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ${LDFLAGS} -a -o ${FATBINARY}
	
release: clean
	mkdir -p ${TARGET}
	env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build ${LDFLAGS} -a -o ${FATBINARY}
	@upx --ultra-brute --best -v -o ${CBINARY} ${FATBINARY} 

clean: 
	go clean
	rm -rf ${TARGET}

