
dummy:
	@echo "makefile requires an argument"

build:
	go build -o editor.exe cmd/editor.go

build-stripped:
	@echo "requires upx which you can install with brew"
	go build -ldflags="-s -w" -o editor-stripped.exe cmd/editor.go
	upx --brute editor-stripped.exe

clean:
	- rm editor.exe
	- rm editor-stripped.exe 