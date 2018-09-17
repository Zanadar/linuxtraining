/*************************************************************************\
*                  Copyright (C) Michael Kerrisk, 2018.                   *
*                                                                         *
* This program is free software. You may use, modify, and redistribute it *
* under the terms of the GNU General Public License as published by the   *
* Free Software Foundation, either version 3 or (at your option) any      *
* later version. This program is distributed without any warranty.  See   *
* the file COPYING.gpl-v3 for details.                                    *
\*************************************************************************/

/* fork_bomb.c

   A fork-bomb program that can be useful when experimenting with
   the cgroups 'pids' controller.
*/
#include <sys/wait.h>
#include "tlpi_hdr.h"

int
main(int argc, char *argv[])
{
    int numChildren, j, failed;
    pid_t childPid;
    int childSleepTime, parentSleepTime;

    if (argc < 2) {
        usageErr("%s num-children [parent-sleep-secs [child-sleep-secs]]\n",
                argv[0]);
    }

    numChildren = (argc > 1) ? atoi(argv[1]) : 1000;
    parentSleepTime = (argc > 2) ? atoi(argv[2]) : 0;
    childSleepTime = (argc > 3) ? atoi(argv[3]) : 300;

    printf("Parent PID = %ld\n", (long) getpid());

    /* If a parent sleep time was specified, show the user the parent's
       PID in case they want to write it to a cgroup.procs file */

    if (parentSleepTime > 0) {
        printf("Parent sleeping for %d seconds\n", parentSleepTime);
        sleep(parentSleepTime);
    }

    printf("Creating %d children that will sleep %d seconds\n",
            numChildren, childSleepTime);

    failed = 0;
    for (j = 1; j <= numChildren && !failed; j++) {
        switch (childPid = fork()) {
        case -1:
            errMsg("fork");
            failed = 1;
            break;
        case 0:
            sleep(childSleepTime);
            exit(EXIT_SUCCESS);
        default:
            printf("Child %d: PID = %ld\n", j, (long) childPid);
            break;
        }
    }

    printf("Waiting for all children to terminate\n");

    while (waitpid(-1, NULL, 0) > 0)
        continue;

    printf("All children terminated; bye!\n");

    exit(EXIT_SUCCESS);
}
