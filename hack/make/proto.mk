.PHONY: boilerplate/proto/%
boilerplate/proto/%: BOILERPLATE_PROTO_DIR=$(shell printf $@ | cut -d'/' -f3- | rev | cut -d'/' -f2- | rev)
boilerplate/proto/%: BOILERPLATE_PROTO_NAME=$(if $(findstring $@,cmd),main,$(shell printf $@ | rev | cut -d/ -f1 | rev | cut -d\. -f1))
boilerplate/proto/%: hack/boilerplate/boilerplate.proto.txt  ## Create protobuf schema file from boilerplate.proto.txt
	@if [ ${BOILERPLATE_PROTO_DIR} != "*.proto" ] && [ ! -d ${BOILERPLATE_PROTO_DIR} ]; then mkdir -p ${BOILERPLATE_PROTO_DIR}; fi
	@cat hack/boilerplate/boilerplate.proto.txt <(printf "package ${BOILERPLATE_PROTO_NAME};\\n") > $*
	@sed -i "s|YEAR|$(shell date '+%Y')|g" $*
