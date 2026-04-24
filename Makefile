.PHONY: init
init:
	git submodule update --init
	go install golang.org/x/perf/cmd/benchstat@latest
	go install github.com/campoy/embedmd@latest
	$(MAKE) lib/libbrotli_cref.a

lib/libbrotli_cref.a: testdata/build_libbrotli_cref.sh
	testdata/build_libbrotli_cref.sh
