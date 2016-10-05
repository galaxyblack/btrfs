package btrfs

import (
	"bytes"
	"fmt"
	"github.com/dennwc/btrfs/ioctl"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const SuperMagic = 0x9123683E

const xattrPrefix = "btrfs."

func Open(path string, ro bool) (*FS, error) {
	if ok, err := IsSubVolume(path); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotBtrfs{Path: path}
	}
	var (
		dir *os.File
		err error
	)
	if ro {
		dir, err = os.OpenFile(path, os.O_RDONLY|syscall.O_NOATIME, 0644)
	} else {
		dir, err = os.Open(path)
	}
	if err != nil {
		return nil, err
	} else if st, err := dir.Stat(); err != nil {
		dir.Close()
		return nil, err
	} else if !st.IsDir() {
		dir.Close()
		return nil, fmt.Errorf("not a directory: %s", path)
	}
	return &FS{f: dir}, nil
}

type FS struct {
	f *os.File
}

func (f *FS) Close() error {
	return f.f.Close()
}

type Info struct {
	MaxID          uint64
	NumDevices     uint64
	FSID           FSID
	NodeSize       uint32
	SectorSize     uint32
	CloneAlignment uint32
}

func (f *FS) Info() (out Info, err error) {
	var arg btrfs_ioctl_fs_info_args
	if err = ioctl.Do(f.f, _BTRFS_IOC_FS_INFO, &arg); err != nil {
		return
	}
	out = Info{
		MaxID:          arg.max_id,
		NumDevices:     arg.num_devices,
		FSID:           arg.fsid,
		NodeSize:       arg.nodesize,
		SectorSize:     arg.sectorsize,
		CloneAlignment: arg.clone_alignment,
	}
	return
}

type DevStats struct {
	WriteErrs uint64
	ReadErrs  uint64
	FlushErrs uint64
	// Checksum error, bytenr error or contents is illegal: this is an
	// indication that the block was damaged during read or write, or written to
	// wrong location or read from wrong location.
	CorruptionErrs uint64
	// An indication that blocks have not been written.
	GenerationErrs uint64
	Unknown        []uint64
}

func (f *FS) GetDevStats(id uint64) (out DevStats, err error) {
	var arg btrfs_ioctl_get_dev_stats
	arg.devid = id
	//arg.nr_items = _BTRFS_DEV_STAT_VALUES_MAX
	arg.flags = 0
	if err = ioctl.Do(f.f, _BTRFS_IOC_GET_DEV_STATS, &arg); err != nil {
		return
	}
	i := 0
	out.WriteErrs = arg.values[i]
	i++
	out.ReadErrs = arg.values[i]
	i++
	out.FlushErrs = arg.values[i]
	i++
	out.CorruptionErrs = arg.values[i]
	i++
	out.GenerationErrs = arg.values[i]
	i++
	if int(arg.nr_items) > i {
		out.Unknown = arg.values[i:arg.nr_items]
	}
	return
}

type FSFeatureFlags struct {
	Compatible   FeatureFlags
	CompatibleRO FeatureFlags
	Incompatible IncompatFeatures
}

func (f *FS) GetFeatures() (out FSFeatureFlags, err error) {
	var arg btrfs_ioctl_feature_flags
	if err = ioctl.Do(f.f, _BTRFS_IOC_GET_FEATURES, &arg); err != nil {
		return
	}
	out = FSFeatureFlags{
		Compatible:   arg.compat_flags,
		CompatibleRO: arg.compat_ro_flags,
		Incompatible: arg.incompat_flags,
	}
	return
}

func (f *FS) GetSupportedFeatures() (out FSFeatureFlags, err error) {
	var arg [3]btrfs_ioctl_feature_flags
	if err = ioctl.Do(f.f, _BTRFS_IOC_GET_SUPPORTED_FEATURES, &arg); err != nil {
		return
	}
	out = FSFeatureFlags{
		Compatible:   arg[0].compat_flags,
		CompatibleRO: arg[0].compat_ro_flags,
		Incompatible: arg[0].incompat_flags,
	}
	//for i, a := range arg {
	//	out[i] = FSFeatureFlags{
	//		Compatible:   a.compat_flags,
	//		CompatibleRO: a.compat_ro_flags,
	//		Incompatible: a.incompat_flags,
	//	}
	//}
	return
}

func (f *FS) GetFlags() (SubvolFlags, error) {
	return iocSubvolGetflags(f.f)
}

func (f *FS) SetFlags(flags SubvolFlags) error {
	return iocSubvolSetflags(f.f, flags)
}

func (f *FS) Sync() (err error) {
	if err = ioctl.Do(f.f, _BTRFS_IOC_START_SYNC, nil); err != nil {
		return
	}
	return ioctl.Do(f.f, _BTRFS_IOC_WAIT_SYNC, nil)
}

func (f *FS) CreateSubVolume(name string) error {
	return CreateSubVolume(filepath.Join(f.f.Name(), name))
}

func (f *FS) DeleteSubVolume(name string) error {
	return DeleteSubVolume(filepath.Join(f.f.Name(), name))
}

func (f *FS) Snapshot(dst string, ro bool) error {
	return SnapshotSubVolume(f.f.Name(), filepath.Join(f.f.Name(), dst), ro)
}

func (f *FS) SnapshotSubVolume(name string, dst string, ro bool) error {
	return SnapshotSubVolume(filepath.Join(f.f.Name(), name),
		filepath.Join(f.f.Name(), dst), ro)
}

func (f *FS) Send(w io.Writer, parent string, subvols ...string) error {
	if parent != "" {
		parent = filepath.Join(f.f.Name(), parent)
	}
	sub := make([]string, 0, len(subvols))
	for _, s := range subvols {
		sub = append(sub, filepath.Join(f.f.Name(), s))
	}
	return Send(w, parent, sub...)
}

func (f *FS) Receive(r io.Reader) error {
	return Receive(r, f.f.Name())
}

func (f *FS) ReceiveTo(r io.Reader, mount string) error {
	return Receive(r, filepath.Join(f.f.Name(), mount))
}

func (f *FS) ListSubvolumes(filter func(Subvolume) bool) ([]Subvolume, error) {
	//root, err := getPathRootID(f)
	//if err != nil {
	//	return nil, fmt.Errorf("can't get rootid for '%s': %v", path, err)
	//}
	m, err := listSubVolumes(f.f)
	if err != nil {
		return nil, err
	}
	out := make([]Subvolume, 0, len(m))
	for _, v := range m {
		if filter != nil && !filter(v) {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

type Compression string

const (
	CompressionNone = Compression("")
	LZO             = Compression("lzo")
	ZLIB            = Compression("zlib")
)

const xattrCompression = xattrPrefix + "compression"

func SetCompression(path string, v Compression) error {
	var value []byte
	if v != CompressionNone {
		var err error
		value, err = syscall.ByteSliceFromString(string(v))
		if err != nil {
			return err
		}
	}
	err := syscall.Setxattr(path, xattrCompression, value, 0)
	if err != nil {
		return &os.PathError{Op: "setxattr", Path: path, Err: err}
	}
	return nil
}

func GetCompression(path string) (Compression, error) {
	var buf []byte
	for {
		sz, err := syscall.Getxattr(path, xattrCompression, nil)
		if err == syscall.ENODATA || sz == 0 {
			return CompressionNone, nil
		} else if err != nil {
			return CompressionNone, &os.PathError{Op: "getxattr", Path: path, Err: err}
		}
		if cap(buf) < sz {
			buf = make([]byte, sz)
		} else {
			buf = buf[:sz]
		}
		sz, err = syscall.Getxattr(path, xattrCompression, buf)
		if err == syscall.ENODATA {
			return CompressionNone, nil
		} else if err == syscall.ERANGE {
			// xattr changed by someone else, and is larger than our current buffer
			continue
		} else if err != nil {
			return CompressionNone, &os.PathError{Op: "getxattr", Path: path, Err: err}
		}
		buf = buf[:sz]
		break
	}
	buf = bytes.TrimSuffix(buf, []byte{0})
	return Compression(buf), nil
}
