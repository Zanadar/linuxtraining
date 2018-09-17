#!/bin/sh
#
# Move all realtime (RT) tasks in the cgroups version 2 unified hierarchy
# to the cgroup root directory. This can be useful because it is not
# possible to enable the "cpu" controller if there are RT tasks in nonroot
# cgroups.  This is restriction is in place because the cgroups v2 "cpu"
# controller does not yet understand how to handle RT tasks.
#
# This script requires no arguments, and must be run as superuser.

# (C) 2017 Michael Kerrisk. Licensed under the GNU GPL v2 or later

cd /sys/fs/cgroup/unified
nonroot_pids=$(cat $(find */ -name cgroup.procs))

LOGFILE=/tmp/rt_tasks.$$.log
for p in $(ps -L -o "pid tid cls rtprio cmd" -p $nonroot_pids |
        awk '$1 == "PID" || $3 == "RR" || $3 == "FF"' | grep -v ’]$’ |
	tee $LOGFILE | awk '$1 != "PID" {print $1}'); do
    echo $p > cgroup.procs
done
if test $(cat $LOGFILE | wc -l) -gt 1; then
    echo "Moved following RT processes to root cgroup:"
    echo
    cat $LOGFILE | sed 's/^/    /'
else
    echo "No RT processes found in nonroot cgroups"
fi
