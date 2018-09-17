/* namespaces_of.go

   Copyright (C) Michael Kerrisk, 2018

   Licensed under GNU General Public License version 3 or later

   Show the namespace memberships of one or more processes in the context
   of the user or PID namespace hierarchy.

   This program does one of the following:
   * If provided with a list of PIDs, this program shows the namespace
     memberships of those processes.
   * If no PIDs are provided, the program shows the namespace memberships
     of all processes on the system (which it discovers by parsing the
     /proc/PID directories).
   * If no PIDs are provided, and the "--subtree <pid>" option is specified,
     then the program shows the subtree of the PID or user namespace hierarchy
     that is rooted at the namespace of the specified PID.

   By default, the program shows namespace memberships in the context of the
   user namespace hierarchy, showing also the non-user namespaces owned by
   each user namespace. If the "--pidns" option is specified, the program
   instead shows just the PID namespace hierarchy.

   The "--no-pids" option suppresses the display of the processes that
   are members of each namespace.

   The "--show-comm" option displays the command being run by each process.

   The "--userns-only" option shows only the user namespace hierarchy,
   omitting other types of namespace.

   The "--all-pids" option can be used in conjunction with "--pidns",
   so that for each process that is displayed, its PID in all of the PID
   namespaces of which it is a member are shown.

   The "--no-color" option can be used to suppress the use of color
   in the displayed output.
*/

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Info from command-lone options

type CmdLineOptions struct {
	useColor      bool
	showComm      bool
	showPids      bool
	showAllPids   bool
	usernsOnly    bool
	showPidnsHier bool
	subtreePID    string
}

// A namespace is uniquely identified by the combination of a device ID
// and an inode number.

type NamespaceID struct {
	device    uint64 // dev_t
	inode_num uint64 // ino_t
}

// For each namespace, we record a number of attributes, beginning with the
// namespace type. In the case of user namespaces, we also record the non-user
// namespaces that the namespace owns as well as the user namespaces that are
// parented by this namespace; alternatively, if the "--pidns_only" option was
// specified, we record just the namespaces that are parented by this
// namespace.  We also record the PIDs of the processes that are members of
// the namespace.

type NamespaceAttribs struct {
	nsType   int           // CLONE_NEW*
	children []NamespaceID // Child+owned namespaces
	pids     []int         // Member processes
}

type NamespaceList map[NamespaceID]*NamespaceAttribs

// The 'NamespaceInfo' structure records information about the namespaces
// that have been discovered:
// * The 'nsList' map records all of the namespaces that we visit.
// * While adding the first namespace to 'nsList', we'll discover the ancestor
//   of all namespaces (the root of the user or PID namespace hierarchy).
//   We record that namespace in 'rootNS'.
// * We may encounter nonuser namespaces whose user namespace owners are not
//   visible because they are ancestors of the user's user namespace (i.e.,
//   this program is being run from a noninitial user namespace).  We record
//   these namespaces as being children of a special entry in the 'nsList'
//   map, 'invisNS'. (The implementation of this special entry presumes that
//   there is no namespace file that has device ID 0 and inode number 0.)

type NamespaceInfo struct {
	nsList  NamespaceList
	rootNS  NamespaceID
	invisNS NamespaceID
}

const INVISIBLE_NS = 0 // 'nsType' for the invisible user namespace entry

// Namespace ioctl() operations.

const NS_GET_USERNS = 0xb701
const NS_GET_PARENT = 0xb702
const NS_GET_NSTYPE = 0xb703

// Namespace types returned by NS_GET_NSTYPE.

const CLONE_NEWNS = 0x00020000
const CLONE_NEWCGROUP = 0x02000000
const CLONE_NEWUTS = 0x04000000
const CLONE_NEWIPC = 0x08000000
const CLONE_NEWUSER = 0x10000000
const CLONE_NEWPID = 0x20000000
const CLONE_NEWNET = 0x40000000

// A list of the names of the symlink files in the /proc/PID/ns directory that
// define a process's namespace memberships.

var allNamespaceSymlinkNames = []string{"cgroup", "ipc", "mnt", "net", "pid",
	"user", "uts"}

// A helpful map to convert a CLONE_NEW* value to a corresponding string
// representation.

var namespaceToString = map[int]string{
	CLONE_NEWCGROUP: "cgroup",
	CLONE_NEWIPC:    "ipc",
	CLONE_NEWNS:     "mnt",
	CLONE_NEWNET:    "net",
	CLONE_NEWPID:    "pid",
	CLONE_NEWUSER:   "user",
	CLONE_NEWUTS:    "uts",
}

// Some color codings for displayed output

const ESC = ""
const RED = ESC + "[31m"
const YELLOW = ESC + "[93m"
const BOLD = ESC + "[1m"
const LIGHT_BLUE = ESC + "[38;5;51m"
const NORMAL = ESC + "(B" + ESC + "[m"
const PID_COLOR = LIGHT_BLUE
const USERNS_COLOR = YELLOW + BOLD

// Create and return a new namespace ID using the device ID and inode
// number of the namespace referred to by 'namespaceFD'.

func NewNamespaceID(namespaceFD int) NamespaceID {
	var sb syscall.Stat_t
	var err error

	// Obtain the device ID and inode number of the namespace file.
	// These values together form the key for the 'nsList' map entry.

	err = syscall.Fstat(namespaceFD, &sb)
	if err != nil {
		fmt.Println("syscall.Fstat(): ", err)
		os.Exit(1)
	}

	return NamespaceID{sb.Dev, sb.Ino}
}

// AddNamespace() adds the namespace referred to by the file descriptor
// 'namespaceFD to the 'nsList' map (creating an entry in the map if one does
// not already exist) and optionally adds the PID specified in 'pid' to the
// list of PIDs that are resident in that namespace.
//
// This function is recursive, calling itself to ensure that an entry is also
// created for the parent or owning namespace of the namespace referred to by
// 'namespaceFD'.  Once that has been done, the namespace referred to by
// 'namespaceFD' is made a child of the parent/owning namespace. Note that,
// except in the case of the initial namespace, a parent/owning namespace must
// exist, since it is pinned into existence by the existence of a child/owned
// namespace (and that namespace is in turn pinned into existence by the open
// file descriptor 'namespaceFD').
//
// 'nsType' is the type of namespace that is being added to 'nsList', and is
// one of the CLONE_NEW* values.
//
// 'pid' is a PID to be added to the list of PIDs resident in this namespace.
// When called recursively to create the ancestor namespace entries, this
// function is called with 'pid' as -1, meaning that no PID needs to be added
// for this namespace entry.
//
// 'showPidnsHier' is set true if we are displaying the PID namespace hierarchy
// rather than the user namespace hierarchy (in which case we must build a
// tree of the PID namespaces, rather than a tree of the user namespaces).
//
// The return value of the function is the ID of the namespace entry
// (i.e., the device ID and inode number corresponding to the namespace
// file referred to by 'namespaceFD').

func (nsi *NamespaceInfo) AddNamespace(namespaceFD int, pid int, nsType int,
	opts CmdLineOptions) NamespaceID {

	ns := NewNamespaceID(namespaceFD)

	if _, fnd := nsi.nsList[ns]; !fnd {

		// Namespace entry does not yet exist in 'nsList' map;
		// create it.

		nsi.nsList[ns] = new(NamespaceAttribs)
		nsi.nsList[ns].nsType = nsType

		// Get a file descriptor for the parent/owning namespace.
		// NS_GET_USERNS returns the owning user namespace when its
		// argument is a non-user namespace, and (conveniently) returns
		// the parent user namespace when its argument is a user
		// namespace. On the other hand, if we are handling only the
		// PID namespace hierarchy, then we must use NS_GET_PARENT to
		// get the parent PID namespace.

		ioctlOp := NS_GET_USERNS
		if opts.showPidnsHier {
			ioctlOp = NS_GET_PARENT
		}

		ret, _, err := syscall.Syscall(syscall.SYS_IOCTL,
			uintptr(namespaceFD), uintptr(ioctlOp), 0)
		parentFD := (int)((uintptr)(unsafe.Pointer(ret)))

		if parentFD == -1 && err == syscall.EPERM &&
			(nsType == CLONE_NEWUSER || ioctlOp == NS_GET_PARENT) {

			// If the current namespace is a user namespace and
			// NS_GET_USERNS failed with EPERM (meaning no visible
			// parent), or we are processing only PID namespaces
			// and NS_GET_PARENT failed with EPERM, then this is
			// the root namespace (or, at least, the topmost
			// visible namespace); remember it.

			nsi.rootNS = ns

		} else if parentFD == -1 && ioctlOp == NS_GET_USERNS {

			fmt.Println("INVIS")

			if err != syscall.EPERM {
				// Unexpected error!
				fmt.Println("ioctl(NS_GET_USERNS): ", err)
				os.Exit(1)
			}

			// If NS_GET_USERNS fails with EPERM, this means
			// that the user namespace that owns this (nonuser)
			// namespace is not visible (i.e., is an ancestor
			// user namespace).  Record these namespaces as
			// children of a special entry in the 'nsList' map.

			if _, fnd := nsi.nsList[nsi.invisNS]; !fnd {

				// The special parent entry does not yet
				// exist; create it.

				nsi.nsList[nsi.invisNS] = new(NamespaceAttribs)
				nsi.nsList[nsi.invisNS].nsType = INVISIBLE_NS
			}

			nsi.nsList[nsi.invisNS].children =
				append(nsi.nsList[nsi.invisNS].children, ns)

		} else {

			// We have a parent/owning namespace; make sure it
			// has an entry in the map. Don't record the process
			// as being a member of parent/owning namespace.

			par := nsi.AddNamespace(parentFD, -1,
				NamespaceType(parentFD), opts)

			// Make the current namespace entry ('ns') a child
			// of the parent/owning namespace entry.

			nsi.nsList[par].children =
				append(nsi.nsList[par].children, ns)

			syscall.Close(parentFD)
		}
	}

	// Add PID to PID list for this namespace entry.

	if pid > 0 {
		nsi.nsList[ns].pids = append(nsi.nsList[ns].pids, pid)
	}

	return ns
}

// NamespaceType() returns a CLONE_NEW* constant telling us what kind of
// namespace is referred to by 'namespaceFD'.

func NamespaceType(namespaceFD int) int {

	ret, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(namespaceFD),
		uintptr(NS_GET_NSTYPE), 0)
	nsType := (int)((uintptr)(unsafe.Pointer(ret)))
	if nsType == -1 {
		fmt.Println("ioctl(NS_GET_NSTYPE)", err)
		os.Exit(1)
	}

	return nsType
}

// AddProcessNamespace() processes a single /proc/PID/ns/* entry, creating a
// namespace entry for that file and, as necessary, namespace entries for all
// ancestor namespaces going back to the initial namespace.  'pid' is a
// string containing a PID; 'nsFile' is a string identifying which namespace
// symlink to open.

func (nsi *NamespaceInfo) AddProcessNamespace(pid string, nsFile string,
	opts CmdLineOptions) {

	// Obtain a file descriptor that refers to the namespace
	// corresponding to 'pid' and 'nsFile'.

	namespaceFD, err := syscall.Open("/proc/"+pid+"/ns/"+nsFile,
		syscall.O_RDONLY, 0)

	if namespaceFD < 0 {
		fmt.Println("open("+"/proc/"+pid+"/ns/"+nsFile+"):", err)
		os.Exit(1)
	}

	// Add namespace entry for this namespace, and all of its ancestor
	// user namespaces.

	npid, _ := strconv.Atoi(pid)
	nsi.AddNamespace(namespaceFD, npid, NamespaceType(namespaceFD), opts)

	syscall.Close(namespaceFD)
}

// PrintAllPIDsFor() looks up the 'NStgid' field in the /proc/PID/status
// file of 'pid' and displays the set of PIDs contained in that field

func PrintAllPIDsFor(pid int, opts CmdLineOptions) {

	sfile := "/proc/" + strconv.Itoa(pid) + "/status"

	file, err := os.Open(sfile)
	if err != nil {
		// Probably, the process terminated between the time we
		// accessed the namespace files and the time we tried to open
		// /proc/PID/status.
		fmt.Print("[can't open " + sfile + "]")
		return
	}

	defer file.Close() // Close file on return from this function.

	re := regexp.MustCompile(":[ \t]*")

	// Scan file line by line, looking for 'NStgid:' entry, and print
	// corresponding set of PIDs.

	s := bufio.NewScanner(file)
	for s.Scan() {
		match, _ := regexp.MatchString("^NStgid:", s.Text())
		if match {
			tokens := re.Split(s.Text(), -1)
			if opts.useColor {
				fmt.Print(PID_COLOR)
			}
			fmt.Print("{ ", tokens[1], " }  ")
			if opts.useColor {
				fmt.Print(NORMAL)
			}

			break
		}
	}
}

// Print a sorted list of the PIDs that are members of a namespace.

func PrintMemberPIDs(indent string, pids []int, opts CmdLineOptions,
	nsType int) {

	// If the namespace has no member PIDs, there's nothing to do. (This
	// could happen if a parent user namespace has no member processes,
	// but has a child user namespace that has a member process.)

	if len(pids) == 0 {
		return
	}

	adj := strings.Repeat(" ", 8)

	sort.Ints(pids)

	if opts.showComm || (opts.showAllPids && nsType == CLONE_NEWPID) {

		// Print processes one per line, with PIDs and (optionally)
		// the name of the command being run by the process.

		for _, pid := range pids {

			fmt.Print(indent + adj)

			if opts.showAllPids && nsType == CLONE_NEWPID {
				PrintAllPIDsFor(pid, opts)

				if !opts.showComm {
					fmt.Println()
				}
			} else {
				if opts.useColor {
					fmt.Print(PID_COLOR)
				}
				fmt.Printf("%-5d  ", pid)
				if opts.useColor {
					fmt.Print(NORMAL)
				}
			}

			if opts.showComm {
				cfile := "/proc/" + strconv.Itoa(pid) + "/comm"

				buf, err := ioutil.ReadFile(cfile)
				if err != nil {
					// Probably, the process terminated
					// between the time we accessed the
					// namespace files and the time we
					// tried to open /proc/PID/comm.
					fmt.Print("[can't open " + cfile + "]")
				} else {
					fmt.Print(string(buf))
				}
			}
		}

	} else {

		// Print PIDs as a list, with multiple PIDs per line.
		// We do a bit of a dance here to produce a list of
		// PIDs that is suitably wrapped and indented, rather
		// than a long single-line list.

		startingCol := len(indent) + len(adj)
		col := startingCol

		for i, pid := range pids {

			if i == 0 || col >= 72 && col > startingCol+32 {
				col = startingCol
				if i > 0 {
					fmt.Println()
				}

				fmt.Print(indent + adj)

				if opts.useColor {
					fmt.Print(PID_COLOR)
				}
				if i == 0 {
					fmt.Print("[ ")
				} else {
					fmt.Print("  ")
				}
			}

			fmt.Print(strconv.Itoa(pid) + " ")
			col += len(strconv.Itoa(pid)) + 1
		}

		fmt.Print("]")
		if opts.useColor {
			fmt.Print(NORMAL)
		}
		fmt.Println()
	}
}

// DisplayNamespaceTree() recursively displays the namespace tree rooted
// at 'ns'. 'level' is our current level in the tree, and is used to produce
// suitably indented output.

func (nsi *NamespaceInfo) DisplayNamespaceTree(ns NamespaceID, level int,
	opts CmdLineOptions) {

	indent := strings.Repeat(" ", level*4)

	// Display the namespace type and ID (device ID + inode number).

	isUserns := nsi.nsList[ns].nsType == INVISIBLE_NS ||
		nsi.nsList[ns].nsType == CLONE_NEWUSER

	if isUserns && opts.useColor {
		fmt.Print(USERNS_COLOR)
	}

	if nsi.nsList[ns].nsType == INVISIBLE_NS {
		fmt.Println("[invisible ancestor user NS]")
	} else {
		fmt.Print(indent+namespaceToString[nsi.nsList[ns].nsType]+" ",
			ns)
		fmt.Println()
	}

	if isUserns && opts.useColor {
		fmt.Print(NORMAL)
	}

	// Optionally display member PIDs for the namespace.

	if opts.showPids {
		PrintMemberPIDs(indent, nsi.nsList[ns].pids, opts,
			nsi.nsList[ns].nsType)
	}

	// Recursively display the child namespaces.

	for _, child := range nsi.nsList[ns].children {
		nsi.DisplayNamespaceTree(child, level+1, opts)
	}
}

// AddNamespacesForAllProcesses() scans /proc/PID directories to build
// namespace entries for all processes on the system.

func (nsi *NamespaceInfo) AddNamespacesForAllProcesses(namespaces []string,
	opts CmdLineOptions) {

	// Fetch a list of the filenames under /proc.

	procFiles, err := ioutil.ReadDir("/proc")
	if err != nil {
		fmt.Println("ioutil.Readdir(): ", err)
		os.Exit(1)
	}

	// Process each /proc/PID (PID starts with a digit).

	for _, f := range procFiles {
		if f.Name()[0] >= '1' && f.Name()[0] <= '9' {
			for _, nsFile := range namespaces {
				nsi.AddProcessNamespace(f.Name(), nsFile, opts)
			}
		}
	}
}

func ShowUsage(status int) {
	fmt.Println(
		`Usage: namespaces_of [options] [--subtree <pid> | <pid>...]

Show the namespace memberships of one or more processes in the context of the
user or PID namespace hierarchy.

This program does one of the following:
* If provided with a list of PIDs, this program shows the namespace
  memberships of those processes.
* If no PIDs are provided, and the '--subtree <pid>' option is not specified,
  the program shows the namespace memberships of all processes on the system.
* If no PIDs are provided, and the '--subtree <pid>' option is specified,
  then the program shows the subtree of the user or PID namespace hierarchy
  that is rooted at the namespace of the specified PID.

By default, the program shows namespace memberships in the context of the user
namespace hierarchy, showing also the non-user namespaces owned by each user
namespace. If the '--pidns' option is specified, the program shows only
the PID namespace hierarchy, omitting other types of namespace.

Options:

--pidns         See above.
--no-pids	Suppress the display of the processes that are members
		of each namespace.
--show-comm	Displays the command being run by each process
--userns-only	Show only the user namespace hierarchy, omitting other
		types of namespace.
--all-pids	For each displayed process show PIDs in all namespaces of
		which the process is a member (used in conjunction with
		'--pidns').
--no-color	Suppress the use of color in the displayed output.

Syntax notes:
* No PID command-line arguments may be supplied when using '--subtree'.
* At most one of '--userns-only' and '--pidns' may be specified.
* '--all-pids' can be specified only in conjunction with '--pidns'.
* '--no-pids' can't be specified in conjunction with either '--show-comm'
  or '--all-pids'.`)

	os.Exit(status)
}

// Parse command-line options and return them conveniently packaged
// in a structure

func ParseCmdLineOptions() CmdLineOptions {

	var opts CmdLineOptions

	// Parse command-line options.

	helpPtr := flag.Bool("help", false, "Show usage message")
	noColorPtr := flag.Bool("no-color", false,
		"Don't use color in output display")
	noPidsPtr := flag.Bool("no-pids", false,
		"Don't show PIDs in namespaces")
	showCommPtr := flag.Bool("show-comm", false,
		"Show command run by each PID")
	usernsOnlyPtr := flag.Bool("userns-only", false,
		"Show only user namespaces")
	allPidsPtr := flag.Bool("all-pids", false,
		"Show all PIDs of each process")
	pidnsPtr := flag.Bool("pidns", false, "Show PID "+
		"namespace hierarchy (instead of user namespace hierarchy")
	subtreePtr := flag.String("subtree", "", "Show namespace subtree "+
		"rooted at namespace of specified process")

	flag.Parse()

	opts.useColor = !*noColorPtr
	opts.showPids = !*noPidsPtr
	opts.showPidnsHier = *pidnsPtr
	opts.showComm = *showCommPtr
	opts.usernsOnly = *usernsOnlyPtr
	opts.showAllPids = *allPidsPtr
	opts.subtreePID = *subtreePtr

	if *helpPtr {
		ShowUsage(0)
	}

	if opts.usernsOnly && opts.showPidnsHier {
		fmt.Println("Combining \"--pidns\" and " +
			"\"--userns-only\" is nonsensical")
		ShowUsage(1)
	}

	if opts.showAllPids && !opts.showPidnsHier {
		fmt.Println("\"--all-pids\" can be specified only with " +
			"\"--pidns\"")
		ShowUsage(1)
	}

	if !opts.showPids && (opts.showComm || opts.showAllPids) {
		fmt.Println("Combining \"--no-pids\" and \"--show-comm\" " +
			"or \"--all-pids\" is nonsensical")
		ShowUsage(1)
	}

	if opts.subtreePID != "" && len(flag.Args()) > 0 {
		fmt.Println("Combining \"--subtree\" with PID arguments " +
			"is nonsensical")
		ShowUsage(1)
	}

	return opts
}

func main() {

	var nsi = NamespaceInfo{nsList: make(NamespaceList),
		invisNS: NamespaceID{0, 0}}

	var opts CmdLineOptions = ParseCmdLineOptions()

	// Determine which namespace symlink files are to be processed.

	nsSymlinks := allNamespaceSymlinkNames
	if opts.usernsOnly {
		nsSymlinks = []string{"user"}
	} else if opts.showPidnsHier {
		nsSymlinks = []string{"pid"}
	}

	// Add namespace entries for specified processes.

	if len(flag.Args()) == 0 || opts.subtreePID != "" {
		nsi.AddNamespacesForAllProcesses(nsSymlinks, opts)
	} else {

		// Add namespaces for PIDs named in the command-line arguments.
		// (flags.Args() is the set of command-line words that were
		// not options.)

		for _, pid := range flag.Args() {
			for _, nsFile := range nsSymlinks {
				nsi.AddProcessNamespace(pid, nsFile, opts)
			}
		}
	}

	if opts.subtreePID == "" {

		// Display the namespace tree rooted at the initial namespace.

		nsi.DisplayNamespaceTree(nsi.rootNS, 0, opts)

		// Display the namespaces owned by (invisible) ancestor user
		// namespaces.

		if _, fnd := nsi.nsList[nsi.invisNS]; fnd &&
			len(nsi.nsList[nsi.invisNS].children) > 0 {
			nsi.DisplayNamespaceTree(nsi.invisNS, 0, opts)
		}

	} else {

		// Display subtree of the namespace hierarchy rooted
		// at the namespace of a specified PID

		symlinkPath := "/proc/" + opts.subtreePID + "/ns/"
		if opts.showPidnsHier {
			symlinkPath += "pid"
		} else {
			symlinkPath += "user"
		}

		namespaceFD, err := syscall.Open(symlinkPath,
			syscall.O_RDONLY, 0)

		if namespaceFD < 0 {
			fmt.Println("Error finding namespace subtree for PID"+
				opts.subtreePID+":", err)
			os.Exit(1)
		}

		nsi.DisplayNamespaceTree(NewNamespaceID(namespaceFD), 0, opts)
	}
}
