package ports

import (
	"context"
	"reflect"
	"testing"

	"torrentstream/internal/domain"
)

func TestEngineInterface(t *testing.T) {
	typ := reflect.TypeOf((*Engine)(nil)).Elem()

	assertMethod(t, typ, "Open", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentSource{}),
	}, []reflect.Type{
		reflect.TypeOf((*Session)(nil)).Elem(),
		errorType(),
	})

	assertMethod(t, typ, "Close", nil, []reflect.Type{errorType()})

	assertMethod(t, typ, "GetSessionState", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
	}, []reflect.Type{
		reflect.TypeOf(domain.SessionState{}),
		errorType(),
	})

	assertMethod(t, typ, "GetSession", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
	}, []reflect.Type{
		reflect.TypeOf((*Session)(nil)).Elem(),
		errorType(),
	})

	assertMethod(t, typ, "ListActiveSessions", []reflect.Type{
		contextType(),
	}, []reflect.Type{
		reflect.SliceOf(reflect.TypeOf(domain.TorrentID(""))),
		errorType(),
	})

	assertMethod(t, typ, "StopSession", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
	}, []reflect.Type{errorType()})

	assertMethod(t, typ, "StartSession", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
	}, []reflect.Type{errorType()})

	assertMethod(t, typ, "RemoveSession", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
	}, []reflect.Type{errorType()})

	assertMethod(t, typ, "SetPiecePriority", []reflect.Type{
		contextType(),
		reflect.TypeOf(domain.TorrentID("")),
		reflect.TypeOf(domain.FileRef{}),
		reflect.TypeOf(domain.Range{}),
		reflect.TypeOf(domain.Priority(0)),
	}, []reflect.Type{errorType()})
}

func TestSessionInterface(t *testing.T) {
	typ := reflect.TypeOf((*Session)(nil)).Elem()

	assertMethod(t, typ, "ID", nil, []reflect.Type{reflect.TypeOf(domain.TorrentID(""))})
	assertMethod(t, typ, "Files", nil, []reflect.Type{reflect.SliceOf(reflect.TypeOf(domain.FileRef{}))})
	assertMethod(t, typ, "SelectFile", []reflect.Type{reflect.TypeOf(0)}, []reflect.Type{reflect.TypeOf(domain.FileRef{}), errorType()})
	assertMethod(t, typ, "SetPiecePriority", []reflect.Type{
		reflect.TypeOf(domain.FileRef{}),
		reflect.TypeOf(domain.Range{}),
		reflect.TypeOf(domain.Priority(0)),
	}, nil)
	assertMethod(t, typ, "Start", nil, []reflect.Type{errorType()})
	assertMethod(t, typ, "Stop", nil, []reflect.Type{errorType()})
	assertMethod(t, typ, "NewReader", []reflect.Type{reflect.TypeOf(domain.FileRef{})}, []reflect.Type{
		reflect.TypeOf((*StreamReader)(nil)).Elem(),
		errorType(),
	})
}

func TestStorageInterface(t *testing.T) {
	typ := reflect.TypeOf((*Storage)(nil)).Elem()

	assertMethod(t, typ, "Size", nil, []reflect.Type{reflect.TypeOf(int64(0))})
	assertMethod(t, typ, "ReadAt", []reflect.Type{contextType(), reflect.TypeOf([]byte{}), reflect.TypeOf(int64(0))}, []reflect.Type{reflect.TypeOf(0), errorType()})
	assertMethod(t, typ, "WriteAt", []reflect.Type{reflect.TypeOf([]byte{}), reflect.TypeOf(int64(0))}, []reflect.Type{reflect.TypeOf(0), errorType()})
	assertMethod(t, typ, "MarkPieceDone", []reflect.Type{reflect.TypeOf(0)}, nil)
	assertMethod(t, typ, "WaitRange", []reflect.Type{contextType(), reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0))}, []reflect.Type{errorType()})
	assertMethod(t, typ, "Close", nil, []reflect.Type{errorType()})
}

func TestSchedulerInterface(t *testing.T) {
	typ := reflect.TypeOf((*Scheduler)(nil)).Elem()

	assertMethod(t, typ, "OnRangeRequest", []reflect.Type{reflect.TypeOf(domain.FileRef{}), reflect.TypeOf(domain.Range{})}, nil)
	assertMethod(t, typ, "Prefetch", []reflect.Type{reflect.TypeOf(domain.FileRef{}), reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0))}, nil)
}

func TestTorrentRepositoryInterface(t *testing.T) {
	typ := reflect.TypeOf((*TorrentRepository)(nil)).Elem()

	assertMethod(t, typ, "Create", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentRecord{})}, []reflect.Type{errorType()})
	assertMethod(t, typ, "Update", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentRecord{})}, []reflect.Type{errorType()})
	assertMethod(t, typ, "Get", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentID(""))}, []reflect.Type{reflect.TypeOf(domain.TorrentRecord{}), errorType()})
	assertMethod(t, typ, "List", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentFilter{})}, []reflect.Type{reflect.SliceOf(reflect.TypeOf(domain.TorrentRecord{})), errorType()})
	assertMethod(t, typ, "GetMany", []reflect.Type{contextType(), reflect.SliceOf(reflect.TypeOf(domain.TorrentID("")))}, []reflect.Type{reflect.SliceOf(reflect.TypeOf(domain.TorrentRecord{})), errorType()})
	assertMethod(t, typ, "Delete", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentID(""))}, []reflect.Type{errorType()})
	assertMethod(t, typ, "UpdateTags", []reflect.Type{contextType(), reflect.TypeOf(domain.TorrentID("")), reflect.SliceOf(reflect.TypeOf(""))}, []reflect.Type{errorType()})
}

func assertMethod(t *testing.T, typ reflect.Type, name string, in []reflect.Type, out []reflect.Type) {
	t.Helper()
	method, ok := typ.MethodByName(name)
	if !ok {
		t.Fatalf("missing method %s", name)
	}

	wantIn := len(in)
	if method.Type.NumIn() != wantIn {
		t.Fatalf("%s NumIn = %d, want %d", name, method.Type.NumIn(), wantIn)
	}
	for i, typIn := range in {
		if got := method.Type.In(i); got != typIn {
			t.Fatalf("%s In[%d] = %s, want %s", name, i, got, typIn)
		}
	}

	if method.Type.NumOut() != len(out) {
		t.Fatalf("%s NumOut = %d, want %d", name, method.Type.NumOut(), len(out))
	}
	for i, typOut := range out {
		if got := method.Type.Out(i); got != typOut {
			t.Fatalf("%s Out[%d] = %s, want %s", name, i, got, typOut)
		}
	}
}

func contextType() reflect.Type {
	return reflect.TypeOf((*context.Context)(nil)).Elem()
}

func errorType() reflect.Type {
	return reflect.TypeOf((*error)(nil)).Elem()
}
