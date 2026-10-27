test:
	go test ./...

vet:
	go vet ./...

lint: vet
	gofmt -l .
