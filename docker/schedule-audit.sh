questionKey="$1"
grep -E "dump diagnosis for.*$questionKey.*\"isRootCausePod\":true" log/koord-scheduler.log| tail -n 1|cut -d "$" -f2-