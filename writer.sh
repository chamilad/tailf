#!/bin/bash

filename=logs/myserver.$(date +%Y_%m_%d).log

while :
do
    line=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w $((100 + RANDOM % 250)) | head -n 1)
    echo "$(date): ${line}" >> $filename
    sleep $((0 + RANDOM % 3))
done

