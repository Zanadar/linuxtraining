/*************************************************************************\
*                  Copyright (C) Michael Kerrisk, 2018.                   *
*                                                                         *
* This program is free software. You may use, modify, and redistribute it *
* under the terms of the GNU General Public License as published by the   *
* Free Software Foundation, either version 3 or (at your option) any      *
* later version. This program is distributed without any warranty.  See   *
* the file COPYING.gpl-v3 for details.                                    *
\*************************************************************************/

/* cpu_burner.c

   Test program for use with cpu_watchdog.c
*/
#include <sys/times.h>
#include <time.h>
#include <signal.h>
#include "tlpi_hdr.h"

static volatile sig_atomic_t gotSig = 0;

static void
handler(int sig)
{
    gotSig = 1;
}

int
main(int argc, char *argv[])
{
    time_t prev;
    struct timespec ts;
    struct timespec curr_rt, prev_rt;
    struct sigaction sa;
    int elapsed_ms;

    sa.sa_handler = handler;
    sa.sa_flags = 0;
    sigemptyset(&sa.sa_mask);
    if (sigaction(SIGTERM, &sa, NULL) == -1)
        errExit("sigaction");
    if (sigaction(SIGINT, &sa, NULL) == -1)
        errExit("sigaction");

    prev = 0;
    if (clock_gettime(CLOCK_REALTIME, &prev_rt) == -1)
        errExit("clock_gettime");

    while (!gotSig) {
        if (clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &ts) == -1)
            errExit("clock_gettime");
        if (ts.tv_sec > prev) {
            if (clock_gettime(CLOCK_REALTIME, &curr_rt) == -1)
                errExit("clock_gettime");

            elapsed_ms = (curr_rt.tv_sec - prev_rt.tv_sec) * 1000 +
                         (curr_rt.tv_nsec - prev_rt.tv_nsec) / 1000000;
            printf("[%ld] %ld (%.3f elapsed)\n",
                    (long) getpid(), (long) ts.tv_sec, elapsed_ms / 1000.0);
            prev = ts.tv_sec;
            prev_rt = curr_rt;
        }
    }

    printf("Bye!\n");
    exit(EXIT_SUCCESS);
}
