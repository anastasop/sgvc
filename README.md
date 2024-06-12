# sgvc

sgvc is a simple version control program, tailored for single files. Suppose you have a project
with `settings.json` or `values.yaml` or `deploy.sh` and you want to have variants of these,
for example `settings-dev.json` or `values-aws.yaml` or `deploy-with-nfs.sh`. You can of course
make copies with different names but this is unmaintainable because short filenames cannot
describe all the changes. Moreover you will have to add `-f options` to build tools. Finally
sometimes you copy such files to other projects and you still want to be able to correlate them
and diff for changes.

sgvc provides version control for single files. You can commit, read, log, diff
and maintain all the versions of a file. The are no repos. Every file is uniquely identified
by the absolute path and everything is stored in a single work dir `$HOME/.cache/sgvc`.

Inspired by Tom Duff's [upd](http://www.iq0.com/duffgram/upd.html).

## Installation

sgvc is written in [go](https://pkg.go.dev). Assuming you have installed the go SDK

```
go install github.com/anastasop/sgvc@latest
```

will install sgvc in the standard place in your PATH. The app stores data in `os.UserCacheDir/sgvc`, which
on Unix is `${HOME}/.cache/sgvc`.

## Usage

Create a file you want under version control

```
$ cd /home/anastasop/src/project1
$ ed deploy.sh
```

Add the file to the index

```
$ sgvc -add 'initial commit' deploy.sh
```

Make a change to the file and commit it

```
$ sgvc -add 'deploy with redis' deploy.sh
```

Check the versions of the file

```
$ sgvc -commits deploy.h
deploy.sh 20240501T00:00:00Z 0001 0000 "initial commit"
deploy.sh 20240501T00:00:00Z 0002 0000 "deploy with redis"
```

Make a change in a previous version, and commit it

```
$ sgvc -cat 1 deploy.sh > deploy.sh # extract the 'initial commit' version. Note the redirection.
$ ed deploy.sh # make the changes
$ sgvc -add 'deploy with nfs' -base 1 deploy.sh # base it on version 1 and commit.
```

See the changes as a list

```
$ sgvc -commits deploy.h
deploy.sh 20240501T00:00:00Z 0001 0000 "initial commit"
deploy.sh 20240501T01:00:00Z 0002 0000 "deploy with redis"
deploy.sh 20240501T02:00:00Z 0003 0001 "deploy with nfs"
```

See the changes as a tree. Indendation means ancestor relationship.

```
$ sgvc -tree deploy.h
deploy.sh 20240501T00:00:00Z 0001 0000 "initial commit"
  deploy.sh 20240501T02:00:00Z 0003 0001 "deploy with nfs"
deploy.sh 20240501T01:00:00Z 0002 0000 "deploy with redis"
```

You can diff versions

```
$ sgvc -diff -from 1 -to 3 deploy.sh
--- /home/anastasop/src/project1/deploy.sh @0001
+++ /home/anastasop/src/project1/deploy.sh @0003
@@ -12, 15 +4 @@
.....
```

Go to another project and use a file from the index

```
$ cd project2
$ sgvc -cat 1 /home/anastasop/src/project1/deploy.sh > deploy.sh
```

## License

Released under the [GPLv3](https://www.gnu.org/licenses/gpl-3.0.en.html).

## Bugs/TODO

- handle corrupted index in case of write failures
- relax dependency on absolute file paths. This is allow to move the index to another directory or use it remotely.
- correlate files in different directories that are based on the same ancestor
- try to eliminate explicit `-base`
