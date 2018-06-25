package profile

import (
	"context"
	"io/ioutil"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
)

type Option func(*manager)

func AsUser(username string) Option {
	return func(o *manager) {
		o.username = username
	}
}

type manager struct {
	appliedOpts bool

	// runs in the user context
	username string
}

func (o *manager) apply(opts ...Option) {
	if o.appliedOpts {
		return
	}
	for _, opt := range opts {
		opt(o)
	}

	o.appliedOpts = true
	return
}

func Install(ctx context.Context, path string, opts ...Option) error {
	o := new(manager)
	o.apply(opts...)

	msg, err := o.execProfileCommand(ctx, "-IF", path)
	if err != nil {
		return errors.Wrap(err, string(msg))
	}
	return nil
}

func (o *manager) execProfileCommand(ctx context.Context, args ...string) (msg []byte, err error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/profiles", args...)
	if err := setuser(cmd, o.username); err != nil {
		return nil, errors.Wrapf(err, "setting exec credentials to user %s", o.username)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "create stdout pipe for profile install command")
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrapf(err, "running command %s", cmd.Args)
	}

	msg, err = ioutil.ReadAll(stdout)
	if err != nil {
		return nil, errors.Wrap(err, "reading stdout pipe output")
	}

	err = cmd.Wait()
	return msg, err
}

func Remove(ctx context.Context, identifier string, opts ...Option) error {
	o := new(manager)
	o.apply(opts...)
	msg, err := o.execProfileCommand(ctx, "-Rp", identifier)
	if err != nil {
		return errors.Wrap(err, string(msg))
	}
	return nil
}

func setuser(cmd *exec.Cmd, username string) error {
	if username == "" {
		return nil
	}
	usr, err := user.Lookup(username)
	if err != nil {
		return errors.Wrapf(err, "looking up username %s", username)
	}
	uid, err := strconv.Atoi(usr.Uid)
	if err != nil {
		return errors.Wrapf(err, "converting user uid %s to int", usr.Uid)
	}

	gid, err := strconv.Atoi(usr.Gid)
	if err != nil {
		return errors.Wrapf(err, "converting user gid %s to int", usr.Gid)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	return nil
}
