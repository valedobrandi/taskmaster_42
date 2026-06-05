#!/bin/bash
count=0
while true; do
    echo "server-demo tick $count"
    count=$((count + 1))
    sleep 1
done
