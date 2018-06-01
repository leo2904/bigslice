// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package bigslice

import (
	"reflect"

	"github.com/grailbio/bigslice/kernel"
	"github.com/grailbio/bigslice/sliceio"
	"github.com/grailbio/bigslice/slicetype"
	"github.com/grailbio/bigslice/sortio"
	"github.com/grailbio/bigslice/typecheck"
)

// Reduce returns a slice that reduces elements pairwise. Reduce
// operations must be commutative and associative. Schematically:
//
//	Reduce(Slice<k, v>, func(v1, v2 v) v) Slice<k, v>
//
// The provided reducer function is invoked to aggregate values of
// type v. Reduce can perform map-side "combining", so that data are
// reduced to their aggregated value aggressively. This can often
// speed up computations significantly.
//
// TODO(marius): Reduce currently maintains the working set of keys
// in memory, and is thus appropriate only where the working set can
// fit in memory. For situations where this is not the case, Cogroup
// should be used instead (at an overhead). Reduce should spill to disk
// when necessary.
//
// TODO(marius): consider pushing combiners into task dependency
// definitions so that we can combine-read all partitions on one machine
// simultaneously.
func Reduce(slice Slice, reduce interface{}) Slice {
	arg, ret, ok := typecheck.Func(reduce)
	if !ok {
		typecheck.Panicf(1, "reduce: invalid reduce function %T", reduce)
	}
	if slice.NumOut() != 2 {
		typecheck.Panic(1, "reduce: input slice must have exactly two columns")
	}
	if arg.NumOut() != 2 || arg.Out(0) != slice.Out(1) || arg.Out(1) != slice.Out(1) || ret.NumOut() != 1 || ret.Out(0) != slice.Out(1) {
		typecheck.Panicf(1, "reduce: invalid reduce function %T, expected func(%s, %s) %s", reduce, slice.Out(1), slice.Out(1), slice.Out(1))
	}
	if !canMakeCombiningFrame(slice) {
		typecheck.Panicf(1, "cannot combine values for keys of type %s", slice.Out(0))
	}
	var hasher kernel.Hasher
	if !kernel.Lookup(slice.Out(0), &hasher) {
		typecheck.Panicf(1, "key type %s is not partitionable", slice.Out(0))
	}
	return &reduceSlice{slice, reflect.ValueOf(reduce), hasher}
}

// ReduceSlice implements "post shuffle" combining merge sort.
type reduceSlice struct {
	Slice
	combiner reflect.Value
	hasher   kernel.Hasher
}

func (r *reduceSlice) Hasher() kernel.Hasher    { return r.hasher }
func (r *reduceSlice) Op() string               { return "reduce" }
func (*reduceSlice) NumDep() int                { return 1 }
func (r *reduceSlice) Dep(i int) Dep            { return Dep{r.Slice, true, true} }
func (r *reduceSlice) Combiner() *reflect.Value { return &r.combiner }

func (r *reduceSlice) Reader(shard int, deps []sliceio.Reader) sliceio.Reader {
	if len(deps) == 1 {
		return deps[0]
	}
	return sortio.Reduce(r, deps, r.combiner)
}

// CanMakeCombiningFrame tells whether the provided Frame type can be
// be made into a combining frame.
func canMakeCombiningFrame(typ slicetype.Type) bool {
	return typ.NumOut() == 2 && kernel.Implements(typ.Out(0), kernel.IndexerInterface)
}