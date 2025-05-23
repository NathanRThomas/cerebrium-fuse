## Cerebrium FUSE Test

Created by Nathan

## Set Up
Setup on Github workspaces is pretty easy

```
sudo apt update
sudo apt install fuse3 -y
```

## Running it
This can be run all from the single main.go file

```
go run main.go
```

## Testing it
I didn't write any go tests for this. But I did leave some debug lines in so you can see when it's hitting cache and when its not.

`cd /mnt/all-projects/project-1/ && python main.py`

will run a simle python script that imports the reused common_lib.py file.

Also 
```
ls /mnt/all-projcts
cat /mnt/all-projcts/project-2/entrypoint.py
```
etc

## Future Thoughts

### Hashing
I kept thinking about how to handle caching using a hash. I think you could hash the file and keep that hash in memory within another global. I think the goal might be to have a ttl on the cached disk data, and if it's expired to then check the hash again to make sure the info is accurate.

I might do something where there's 2 timestamps. One to know the data is "stale" but is still ok to return, and another to know the data is actually too old and to pretend it's not cached.

That way you can return "slightly" old data to the user right away, and then fire a background process to read from disk to make sure the has is the same, and if so update the timestamps again.
Something where it's stale after 1 minute, but good for 5 minutes or something.  So you can hot load data in the background while not hurting performance the user sees.

### Cache Size Limits
I think you'll want a balance of the last time something was accessed, how frequently it's accessed, and how large it is. 

Things that are "large" took a long time to cache, so you'll really want to make sure you don't need them anymore before you remove them.

You also don't want to expire one of your most used items just because someone needs 100 new items loaded once.

If you're really tight on size for the cache I would set it up where it wouldn't even cache the first time a file is requested. We'd keep track in memory something like

```
type cacheObject struct {
    size uint64
    accessed []time.TIme
}

var cachedObjects map[string]cacheObject
```
so when it gets requested you add the path to your map, and then set the first accessed time.
If something only been accessed once in the last say hour, don't cache it.

Each time it's requested add to the accessed list. Limit that length to something like 10 is probably all we need.

Each time something gets cached, because now there's enough requests over a period of time, trigger a background thread looking for objects that don't need to be cached anymore and delete them. This process can clear out cached data that doesn't meet our requirements for being "important" anymore.
