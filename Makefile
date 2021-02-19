PROG_NAME=olm-mermaid-graph
MERMAID_TEMP_SCRIPT=mermaid.mer
.PHONY: all
all: build

.PHONY: build
build:
	go build -o bin/$(PROG_NAME) ./

.PHONY: install
install:
	go install

run: build
	sqlite3 -bail -init sqlite3.sql 2>/dev/null | bin/$(PROG_NAME) 1>$(MERMAID_TEMP_SCRIPT)
	docker pull minlag/mermaid-cli
	docker run -v $(PWD)/$(MERMAID_TEMP_SCRIPT):/$(MERMAID_TEMP_SCRIPT) -v /tmp:/tmp -it docker.io/minlag/mermaid-cli:latest -i /$(MERMAID_TEMP_SCRIPT) -o /tmp/$(MERMAID_TEMP_SCRIPT).png
	echo "output PNG file is /tmp/$(MERMAID_TEMP_SCRIPT).png"
	open /tmp/$(MERMAID_TEMP_SCRIPT).png

.PHONY: clean
clean:
	rm -r bin
	rm mermaid.mer
	rm /tmp/mermaid.mer.png

