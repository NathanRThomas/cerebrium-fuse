/** ****************************************************************************************************************** **
	Created for the Cerebrium FUSE Test
	2025-05-23 Nathan

	I'm only caching the files in the ssd folder
	all directory lookups are still happening in the nfs directory

** ****************************************************************************************************************** **/

package main

import (
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

//-------------------------------------------------------------------------------------------------------------------//
//----- CONSTS ------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------//

const pwd = "/workspaces/cerebrium-fuse/"
const cacheDir = pwd + "ssd/"

var ErrIsDir = errors.New("dir cannot be read")

//-------------------------------------------------------------------------------------------------------------------//
//----- STRUCTS -----------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------//

var subDirs map[string][]Dir // keeps track of the sub directories for each path
var count nextInod

// just going with a sequential unique file id approach for this
type nextInod struct {
	inode uint64
	mu    sync.RWMutex
}

// thread safe, gives the next id for a file or dir
func (n *nextInod) next() (ret uint64) {
	n.mu.Lock()
	n.inode++
	ret = n.inode
	n.mu.Unlock()

	return
}

// FS implements our nfs/ssd implementation
type FS struct{}

func (FS) Root() (fs.Node, error) {
	return Dir{inode: count.next(), path: pwd + "nfs/"}, nil
}

// root dir for our cached file location
type Dir struct {
	name, path string
	inode      uint64
}

func (d Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = d.inode
	a.Mode = os.ModeDir | 0o555 // leaving these as read only
	return nil
}

func (d Dir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	// fmt.Println("TOP Lookup", d.path, name)

	// see if we're looking for a known sub-directory
	for _, dir := range subDirs[d.path] {
		if dir.name == name {
			// this is a directory and we already have the info
			return dir, nil
		}
	}

	// not a known directory
	// try to find this file, cached or not
	file, err := findFile(ctx, d.path, name)
	if err == nil {
		// we got it
		return file, nil
	}

	// the above didn't work

	// see if this was actually a directory
	if errors.Is(err, ErrIsDir) {
		// fmt.Println("returning dir", d.path, name)
		return readDir(d.path, name)
	}

	// fmt.Println("we didn't find anything")

	// do the same not exist check again
	if os.IsNotExist(err) {
		return nil, syscall.ENOENT // dude, this file just doesn't exist
	}

	// if something else fails here we're going to want to know about it
	slog.Error(err.Error())

	return nil, syscall.ENOENT // we do want to return something
}

func (d Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	files, err := os.ReadDir(d.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read path dir: %s : %w", d.path, err)
	}

	dirDirs := make([]fuse.Dirent, 0, len(files))          // this is our return, init with some capacity for speed
	subDirs[d.path] = make([]Dir, 0, len(subDirs[d.path])) // clear this so we re-populate it, give capacity based on what we'd expect it to be from last time

	for _, file := range files {
		fullPath := filepath.Join(d.path, file.Name()) // always code like someone will review it

		// Use os.Stat to get os.FileInfo
		info, err := os.Stat(fullPath)
		if err != nil {
			slog.Warn(fmt.Sprintf("error stating file: %s : %v:", fullPath, err))
			continue
		}

		dir := fuse.Dirent{
			Inode: count.next(),
			Name:  info.Name(),
		}

		if info.IsDir() {
			dir.Type = fuse.DT_Dir

			// add this as a sub-directory list for this path
			subDirs[d.path] = append(subDirs[d.path], Dir{name: file.Name(), path: fullPath, inode: count.next()})
		} else {
			dir.Type = fuse.DT_File
		}

		dirDirs = append(dirDirs, dir)
	}

	return dirDirs, nil
}

// File implements both Node and Handle for the hello file.
type File struct {
	path, cachedPath string
	inode, size      uint64
}

// this makes it a fs.Node interface
func (f File) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = f.inode
	a.Mode = 0444
	a.Size = f.size
	return nil
}

// Reads from the cached location first, otherwise from the slower disk
func (f File) ReadAll(ctx context.Context) ([]byte, error) {
	if len(f.cachedPath) > 0 {
		fmt.Println("read from cache", f.cachedPath)
		// do the cached path
		if data, err := os.ReadFile(f.cachedPath); err == nil {
			return data, nil
		} else {
			slog.Error(fmt.Sprintf("Error reading from cache location: %s : %v", f.cachedPath, err))
		}
		// fall back to the non-cached path
	}

	fmt.Println("pausing to sim slow drives")
	time.Sleep(time.Millisecond * 500) // let's punish the slow reads here

	fmt.Println("read from non-cache", f.path)
	// not cached, so return it here
	return os.ReadFile(f.path)
}

//-------------------------------------------------------------------------------------------------------------------//
//----- FUNCTIONS ---------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------//

// main entry point for finding our File object from either our slow or cached disks
func findFile(ctx context.Context, path, name string) (*File, error) {

	// init our file object
	file := &File{
		path:  filepath.Join(path, name), // always code like someone will review it
		inode: count.next(),
	}

	cachedPath := filepath.Join(cacheDir, name) // this is where we'd expect it to be cached

	// let's see if it's already cached
	info, err := os.Stat(cachedPath)
	if err == nil {
		// this is great, file is already cached
		file.cachedPath = cachedPath // set the cache location

	} else {
		if os.IsNotExist(err) == false {
			// we want to know if this sort of error happens
			slog.Error(fmt.Sprintf("unable to stat cached directory location: %s", cachedPath))
		}

		// try the slow disk location
		info, err = os.Stat(file.path)
		if err != nil {
			return nil, err // that's a 404
		}
	}

	// make sure it's what we're looking for
	if info.Mode().IsRegular() == false {
		return nil, ErrIsDir // we're looking for files here
	}

	// otherwise, we're good and done
	// we want to pull all the contents as well
	content, err := file.ReadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("error reading contents of file: %s : %s : %v", path, name, err)
	}

	file.size = uint64(len(content)) // get the size of the file

	// at this point we're good to return the File object as it has what we need
	// but we sould also take this chance to cache it if it wasn't already cached
	if file.cachedPath == "" {
		go func() {
			err = os.WriteFile(cachedPath, content, 0664)
			if err != nil {
				slog.Error(fmt.Sprintf("error writing file to cache: %s : %v", cachedPath, err))
				return
			}

			// else we got it cached now
			file.cachedPath = cachedPath // save this for the next read
		}()
	}

	return file, nil // we're good
}

func readDir(path, name string) (Dir, error) {
	fullPath := filepath.Join(path, name) // always code like someone will review it

	info, err := os.Stat(fullPath)
	if err != nil {
		return Dir{}, fmt.Errorf("error stating dir: %s : %v", fullPath, err)
	}

	switch {
	case info.Mode().IsDir():
		// this is good
	default:
		return Dir{}, fmt.Errorf("was expecting a dir and didn't get one: %s", fullPath)
	}

	// fmt.Println("Creating Dir", count.next(), name, path)

	return Dir{
		inode: count.next(),
		name:  name,
		path:  fullPath,
	}, nil
}

// this ensures the directory exists and clears the contents if it does
// used for reseting the cache ssd directory
func createDirIfNotExist(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		// this already existed, so let's clear the contents
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("we couldn't remove the dir : %s : %v", dir, err)
		}

	} else if os.IsNotExist(err) {
		// we're good, already doesn't exit

	} else {
		return err // return any other error we had
	}

	return os.MkdirAll(dir, 0777) // make a fresh dir
}

//----- ENTRY ---------------------------------------------------------------------------------------------------//

func main() {
	slog.Info("Starting FUSE")

	// alloc some memory for our global map
	subDirs = make(map[string][]Dir)

	// make sure our cache directory exists
	if err := createDirIfNotExist(cacheDir); err != nil {
		log.Fatal(err) // not much we can do here
	}

	c, err := fuse.Mount(
		"/mnt/all-projects/",
		fuse.FSName("cerebrium test"),
	)
	if err != nil {
		log.Fatal(err) // we're done if this doesn't work
	}
	defer c.Close()

	// kept getting stuck mounted so going to try to handle this gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// create a server first
	server := fs.New(c, nil)
	go func() {
		err := server.Serve(FS{}) // so this can do it's thing in another thread
		if err != nil {
			slog.Error(fmt.Sprintf("Serve error: %v", err))
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	slog.Info("interrupted, unmounting...")

	// do the unmount
	if err := fuse.Unmount("/mnt/all-projects"); err != nil {
		slog.Error(fmt.Sprintf("Unmount error: %v", err))
	}

	slog.Info("fuse exiting...")
}
