#!/bin/bash

cp -r ./template_service "$1"
for file in $(find "./$1" -type f); do
  sed -i '.bak' "s/template_service/template/g" "$file"
  sed -i '.bak' "s/template/$1/g" "$file"
done

find . -type f -name '*.bak' -delete
