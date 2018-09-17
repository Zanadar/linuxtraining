/*************************************************************************\
*                  Copyright (C) Michael Kerrisk, 2018.                   *
*                                                                         *
* This program is free software. You may use, modify, and redistribute it *
* under the terms of the GNU General Public License as published by the   *
* Free Software Foundation, either version 3 or (at your option) any      *
* later version. This program is distributed without any warranty.  See   *
* the file COPYING.gpl-v3 for details.                                    *
\*************************************************************************/

/* pending_sig_expt.c

   Write a program, pending_sig_expt.c, that:

   * Establishes a handler for SIGINT that does nothing but return
   * Blocks all signals except SIGINT (sigprocmask())
   * Calls pause() to wait for a signal
   * After pause() returns, retrieves the list of pending signals for
     the process (sigpending()) and prints them (strsignal())

   Run the program and send it various signals (other than SIGINT)
   using the kill command. (Send signals whose default disposition is
   not "ignore".) Then type Control-C to generate SIGINT and inspect
   the list of pending signals.

   Extend the program created in the preceding exercise so that:
   * Just after installing the handler for SIGINT, it installs an
     additional handler for the SIGQUIT signal (generated when the
     Control-\ key is pressed). The handler should print a message
     "SIGQUIT received", and return.
   * After displaying the list of pending signals, unblock SIGQUIT
     and call pause() once more.

   While the program is blocking signals (i.e., before typing
   Control-C) try typing Control-\ multiple times. After Control-C
   is typed, how many times does the SIGQUIT handler display its
   message? Why?
*/
#define _GNU_SOURCE
#include <signal.h>
#include "tlpi_hdr.h"

/* FIXME: Implement handlers for SIGINT and SIGQUIT; the latter handler
   should display a message that SIGQUIT has been caught */
static void sigIntHandler(int sig) {
  printf("\nheya! handling %d\n", sig);
  return;
}

static void sigQuitHandler(int sig) {
  printf("\nhandling SIGQUIT %d\n", sig);
  return;
}

int main(int argc, char *argv[])
{

    /* FIXME: Add variable declarations as required */
    sigset_t blocking, prev, pending;
    struct sigaction sa;
    int err;

    /* FIXME: Block all signals except SIGINT */
    err = sigfillset(&blocking);
    if (err == -1)
      errExit("sigfillset");
    err = sigdelset(&blocking, SIGINT);
    sigprocmask(SIG_BLOCK, &blocking, NULL);

    /* FIXME: Set up handlers for SIGINT and SIGQUIT */
    sa.sa_flags = 0;
    sa.sa_handler = sigIntHandler;
    sigemptyset(&sa.sa_mask);
    if (sigaction(SIGINT, &sa, NULL) == -1)
      errExit("sigaction");

    sa.sa_handler = sigQuitHandler;
    if (sigaction(SIGQUIT, &sa, NULL) == -1)
      errExit("sigaction");

    /* Block until SIGINT handler is invoked */

    printf("Pausing... send me some signals (PID=%ld)\n", (long) getpid());
    pause();
    printf("UnPausing...");
    err = sigpending(&pending);
    if (err == -1)
      errExit("sigpending");

    printf("\nPending signals: \n")
    for(int i = 1; i < NSIG; i++){
      if (sigismember(&pending, i))
        printf("\nsignal %d : %s\n", i, strsignal(i));
    }
    /* FIXME: Retrieve and display set of pending signals */

    /* FIXME: Unblock SIGQUIT */

    err = sigdelset(&blocking, SIGQUIT);
    sigprocmask(SIG_SETMASK, &blocking, NULL);

    pause();

    exit(EXIT_SUCCESS);
}
