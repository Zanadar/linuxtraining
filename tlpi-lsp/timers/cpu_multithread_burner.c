/*************************************************************************\
*                  Copyright (C) Michael Kerrisk, 2018.                   *
*                                                                         *
* This program is free software. You may use, modify, and redistribute it *
* under the terms of the GNU General Public License as published by the   *
* Free Software Foundation, either version 3 or (at your option) any      *
* later version. This program is distributed without any warranty.  See   *
* the file COPYING.gpl-v3 for details.                                    *
\*************************************************************************/

/* cpu_multithread_burner.c

   Usage: cpu_multithread_burner period...

   This program creates one thread per command-line argument.
   Each thread loops consuming CPU, and, after each 'period' seconds
   of consumed CPU, reports its thread ID, total CPU time consumed,
   and rate of CPU consumption per real second since the last report.

   For some experiments, it is useful to confine all threads to the
   same CPU, using taskset(1). For example:

        taskset 0x1 ./cpu_multithread_burner 2 2

   See also cpu_multi_burner.c.
*/
#define _GNU_SOURCE
#include <sys/syscall.h>
#include <sched.h>
#include <sys/times.h>
#include <time.h>
#include <signal.h>
#include <pthread.h>
#include "tlpi_hdr.h"

static pid_t
gettid(void)
{
    return syscall(SYS_gettid);
}

#define NANO 1000000000

static void *
threadFunc(void *arg)
{
    int prev, curr;
    struct timespec curr_cpu;
    struct timespec curr_rt, prev_rt;
    int elapsed_us;
    float period;
    char *sarg = arg;

    sscanf(sarg, "%f", &period);

    prev = 0;

    if (clock_gettime(CLOCK_REALTIME, &prev_rt) == -1)
        errExit("clock_gettime");

    while (1) {
        if (clock_gettime(CLOCK_THREAD_CPUTIME_ID, &curr_cpu) == -1)
            errExit("clock_gettime");

        curr = curr_cpu.tv_sec / period + curr_cpu.tv_nsec / period / NANO;

        if (clock_gettime(CLOCK_REALTIME, &curr_rt) == -1)
            errExit("clock_gettime");

        elapsed_us = (curr_rt.tv_sec - prev_rt.tv_sec) * 1000000 +
                     (curr_rt.tv_nsec - prev_rt.tv_nsec) / 1000;

        if (curr > prev) {
            printf("[%ld]  CPU: %.3f; elapsed/cpu = %0.3f; %%CPU = %.3f\n",
                    (long) gettid(),
                    (float) curr * period,
                    elapsed_us / 1000000.0 / period,
                    period / (elapsed_us / 1000000.0) * 100.0);
            prev = curr;
            prev_rt = curr_rt;
        }
    }

    return NULL;
}

int
main(int argc, char *argv[])
{
    pthread_t thr;
    int j, s;

    if (argc < 2 || strcmp(argv[1], "--help") == 0)
        usageErr("%s [period]...\n"
                "Creates one thread per argument that reports "
                "CPU time each 'period' CPU seconds\n"
                "'period' can be a floating-point number\n", argv[0]);

    /* Create one thread per command-line argument */

    for (j = 1; j < argc; j++) {
        s = pthread_create(&thr, NULL, threadFunc, argv[j]);
        if (s != 0)
            errExitEN(s, "pthread_create");
    }

    pause();

    exit(EXIT_SUCCESS);
}
