#!/usr/bin/env bash
for folder in  api controllers pkg test ; do
   echo "./$folder"
  find ./$folder -name "*.go" | grep -v "_test.go" | grep -v "_moq.go" | grep -v "zz_" | grep -v "gin" | sed 's/.go//g'|xargs -I{} sh -c "echo {}; if ! test -f '{}_test.go'; then sed '/^package/q' {}.go > {}_test.go; fi"
done
rm -f {}_test.go
