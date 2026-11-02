package dockerdeploy

import (
	"archive/tar"
	"context"
	"path"
	"strings"
)

const imageDestinationNodeLimitV1 = 1000000
const imageDestinationMetadataLimitV1 = 64 << 20

type imageDestinationNodeV1 struct {
	name, link string
	kind       byte
}

// Shared by all retained layers and the working index. Charge before retaining
// entries, including implicit parents and extension-header payloads.
type imageDestinationBudgetV1 struct{ nodes, bytes int }

func (b *imageDestinationBudgetV1) charge(nodes, size int) error {
	if nodes < 0 || size < 0 || nodes > imageDestinationNodeLimitV1-b.nodes || size > imageDestinationMetadataLimitV1-b.bytes {
		return invalidImageIdentityArchiveV1()
	}
	b.nodes += nodes
	b.bytes += size
	return nil
}

func (b *imageDestinationBudgetV1) entry(header *tar.Header, name string) (imageDestinationNodeV1, error) {
	if len(header.Linkname) > 4096 || strings.ContainsAny(header.Linkname, "\x00\\") {
		return imageDestinationNodeV1{}, invalidImageIdentityArchiveV1()
	}
	base := path.Base(name)
	if strings.HasPrefix(base, ".wh.") && (header.Typeflag != tar.TypeReg || header.Size != 0 || base == ".wh.") {
		return imageDestinationNodeV1{}, invalidImageIdentityArchiveV1()
	}
	if base == ".wh.." || base == ".wh..." {
		return imageDestinationNodeV1{}, invalidImageIdentityArchiveV1()
	}
	if err := b.charge(1, len(name)+len(header.Linkname)); err != nil {
		return imageDestinationNodeV1{}, err
	}
	return imageDestinationNodeV1{name: name, link: header.Linkname, kind: header.Typeflag}, nil
}

type imageDestinationIndexV1 map[string]imageDestinationNodeV1

func (index imageDestinationIndexV1) remove(ctx context.Context, prefix string, childrenOnly bool) error {
	for name := range index {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !childrenOnly && name == prefix || prefix == "." && name != "." || strings.HasPrefix(name, prefix+"/") {
			delete(index, name)
		}
	}
	return nil
}

func (index imageDestinationIndexV1) apply(ctx context.Context, entries []imageDestinationNodeV1, budget *imageDestinationBudgetV1) error {
	// Whiteouts affect lower layers, regardless of their position relative to
	// same-layer additions in the tar stream.
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		base := path.Base(entry.name)
		if strings.HasPrefix(base, ".wh.") {
			for parent := path.Dir(entry.name); parent != "."; parent = path.Dir(parent) {
				if node, exists := index[parent]; exists && node.kind != tar.TypeDir {
					return invalidImageIdentityArchiveV1()
				}
			}
		}
		if base == ".wh..wh..opq" {
			if err := index.remove(ctx, path.Dir(entry.name), true); err != nil {
				return err
			}
		} else if strings.HasPrefix(base, ".wh.") {
			if err := index.remove(ctx, path.Join(path.Dir(entry.name), base[4:]), false); err != nil {
				return err
			}
		}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(path.Base(entry.name), ".wh.") {
			continue
		}
		for parent := path.Dir(entry.name); parent != "."; parent = path.Dir(parent) {
			if node, exists := index[parent]; exists && node.kind != tar.TypeDir {
				return invalidImageIdentityArchiveV1()
			}
		}
		if previous, exists := index[entry.name]; exists && (previous.kind != tar.TypeDir || entry.kind != tar.TypeDir) {
			if err := index.remove(ctx, entry.name, false); err != nil {
				return err
			}
		}
		index[entry.name] = entry
		for parent := path.Dir(entry.name); parent != "."; parent = path.Dir(parent) {
			if _, exists := index[parent]; !exists {
				if err := budget.charge(1, len(parent)); err != nil {
					return err
				}
				index[parent] = imageDestinationNodeV1{name: parent, kind: tar.TypeDir}
			}
		}
	}
	return nil
}

func (index imageDestinationIndexV1) destination(ctx context.Context, target string) (resolved, kind string, content bool, err error) {
	if !path.IsAbs(target) || path.Clean(target) != target || target == "/" || len(target) > 4096 || strings.ContainsAny(target, "\\\x00\r\n\t") {
		return "", "", false, invalidImageIdentityArchiveV1()
	}
	resolved = target
	for hop := 0; hop <= 32; hop++ {
		if err := ctx.Err(); err != nil {
			return "", "", false, err
		}
		parts := strings.Split(strings.TrimPrefix(resolved, "/"), "/")
		changed := false
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			node, exists := index[prefix]
			if !exists {
				continue
			}
			if node.kind == tar.TypeSymlink {
				if node.link == "" {
					return "", "", false, invalidImageIdentityArchiveV1()
				}
				destination := node.link
				if !path.IsAbs(destination) {
					destination = path.Join("/", path.Dir(prefix), destination)
				}
				resolved = path.Join(destination, path.Join(parts[i+1:]...))
				if resolved == "/" || len(resolved) > 4096 {
					return "", "", false, invalidImageIdentityArchiveV1()
				}
				changed = true
				break
			}
			if node.kind != tar.TypeDir {
				if i == len(parts)-1 {
					return resolved, "non-directory", true, nil
				}
				return "", "", false, invalidImageIdentityArchiveV1()
			}
		}
		if changed {
			continue
		}
		name := strings.TrimPrefix(resolved, "/")
		kind = "absent"
		if _, exists := index[name]; exists {
			kind = "directory"
		}
		for candidate, node := range index {
			if err := ctx.Err(); err != nil {
				return "", "", false, err
			}
			if strings.HasPrefix(candidate, name+"/") && node.kind != tar.TypeDir {
				return resolved, kind, true, nil
			}
		}
		return resolved, kind, false, nil
	}
	return "", "", false, invalidImageIdentityArchiveV1()
}
