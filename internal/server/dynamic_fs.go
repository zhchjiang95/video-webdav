// Package server 内部使用的动态文件系统代理，支持热更新视频目录与过滤规则。
package server

import (
	"context"
	"os"
	"sync/atomic"

	"golang.org/x/net/webdav"
)

// dynamicFS 动态代理 webdav.FileSystem，支持原子替换底层文件系统。
type dynamicFS struct {
	curr atomic.Pointer[webdav.FileSystem]
}

func newDynamicFS(initial webdav.FileSystem) *dynamicFS {
	d := &dynamicFS{}
	d.curr.Store(&initial)
	return d
}

// Update 更新底层文件系统指针。
func (d *dynamicFS) Update(fs webdav.FileSystem) {
	d.curr.Store(&fs)
}

func (d *dynamicFS) get() webdav.FileSystem {
	return *d.curr.Load()
}

func (d *dynamicFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return d.get().Mkdir(ctx, name, perm)
}

func (d *dynamicFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	return d.get().OpenFile(ctx, name, flag, perm)
}

func (d *dynamicFS) RemoveAll(ctx context.Context, name string) error {
	return d.get().RemoveAll(ctx, name)
}

func (d *dynamicFS) Rename(ctx context.Context, oldName, newName string) error {
	return d.get().Rename(ctx, oldName, newName)
}

func (d *dynamicFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return d.get().Stat(ctx, name)
}

