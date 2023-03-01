
dummy:
	@echo "makefile requires an argument"

build:
	go build -o editor.exe

build-stripped:
	@echo "requires upx which you can install with brew"
	go build -ldflags="-s -w" -o editor-stripped.exe
	upx --brute editor-stripped.exe

clean:
	- rm editor.exe
	- rm editor-stripped.exe 