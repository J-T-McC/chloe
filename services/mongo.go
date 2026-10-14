package services

import (
	"context"

	"chloe/internal/core"
	"chloe/internal/mdb"
)

// MongoService backs the MongoDB collection browser, document editors, designers and shells.
type MongoService struct{ M *core.Manager }

func (s *MongoService) client(connID string) (*mdb.Client, error) { return s.M.Mongo(connID) }

func (s *MongoService) Databases(ctx context.Context, connID string) ([]mdb.DBInfo, error) {
	return s.M.MongoDatabases(ctx, connID)
}

// CreateDatabase creates db together with its first collection.
func (s *MongoService) CreateDatabase(ctx context.Context, connID, db, collection string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.CreateDatabase(ctx, db, collection)
}

func (s *MongoService) DropDatabase(ctx context.Context, connID, db string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.DropDatabase(ctx, db)
}

// Collections lists collections and views; with stats, also counts and sizes.
func (s *MongoService) Collections(ctx context.Context, connID, db string, stats bool) ([]mdb.CollInfo, error) {
	c, err := s.client(connID)
	if err != nil {
		return nil, err
	}
	return c.Collections(ctx, db, stats)
}

func (s *MongoService) CreateCollection(ctx context.Context, connID, db, name string, o mdb.CollOptions) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.CreateCollection(ctx, db, name, o)
}

// CreateScript previews the command that creates a collection or view.
func (s *MongoService) CreateScript(name string, o mdb.CollOptions) (string, error) {
	cmd, err := mdb.CreateCommand(name, o)
	if err != nil {
		return "", err
	}
	return "db.runCommand(" + mdb.Format(mdb.ToVal(cmd), "  ", false) + ")", nil
}

func (s *MongoService) DropCollection(ctx context.Context, connID, db, name string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.DropCollection(ctx, db, name)
}

func (s *MongoService) EmptyCollection(ctx context.Context, connID, db, name string) (int64, error) {
	c, err := s.client(connID)
	if err != nil {
		return 0, err
	}
	return c.EmptyCollection(ctx, db, name)
}

func (s *MongoService) RenameCollection(ctx context.Context, connID, db, from, to string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.RenameCollection(ctx, db, from, to)
}

func (s *MongoService) DuplicateCollection(ctx context.Context, connID, db, from, to string, data bool) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.DuplicateCollection(ctx, db, from, to, data)
}

func (s *MongoService) SetValidation(ctx context.Context, connID, db, name string, o mdb.CollOptions) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.SetValidation(ctx, db, name, o)
}

func (s *MongoService) SetViewPipeline(ctx context.Context, connID, db, name, viewOn, pipeline string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.SetViewPipeline(ctx, db, name, viewOn, pipeline)
}

func (s *MongoService) Indexes(ctx context.Context, connID, db, coll string) ([]mdb.IndexInfo, error) {
	c, err := s.client(connID)
	if err != nil {
		return nil, err
	}
	return c.Indexes(ctx, db, coll)
}

func (s *MongoService) CreateIndex(ctx context.Context, connID, db, coll string, spec mdb.IndexSpec) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.CreateIndex(ctx, db, coll, spec)
}

// IndexScript previews the createIndex call for spec.
func (s *MongoService) IndexScript(coll string, spec mdb.IndexSpec) (string, error) {
	d, err := mdb.IndexDoc(spec)
	if err != nil {
		return "", err
	}
	v := mdb.ToVal(d)
	keys, opts := v.F[0].V, mdb.Val{T: "object", F: v.F[1:]}
	return "db.getCollection(" + mdb.Format(mdb.Val{T: "string", V: coll}, "", false) + ").createIndex(" +
		mdb.Format(keys, "", false) + ", " + mdb.Format(opts, "", false) + ")", nil
}

func (s *MongoService) DropIndex(ctx context.Context, connID, db, coll, name string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.DropIndex(ctx, db, coll, name)
}

// ---- documents ------------------------------------------------------------------------

func (s *MongoService) Find(ctx context.Context, connID string, q mdb.Query, count bool) (mdb.Page, error) {
	c, err := s.client(connID)
	if err != nil {
		return mdb.Page{}, err
	}
	return c.Find(ctx, q, count)
}

func (s *MongoService) GetDoc(ctx context.Context, connID, db, coll string, id mdb.Val) (mdb.Val, error) {
	c, err := s.client(connID)
	if err != nil {
		return mdb.Val{}, err
	}
	return c.GetDoc(ctx, db, coll, id)
}

// InsertDocs inserts a document or an array of documents written in shell syntax.
func (s *MongoService) InsertDocs(ctx context.Context, connID, db, coll, text string) (int, error) {
	c, err := s.client(connID)
	if err != nil {
		return 0, err
	}
	return c.InsertDocs(ctx, db, coll, text)
}

func (s *MongoService) ReplaceDoc(ctx context.Context, connID, db, coll string, id mdb.Val, text string) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.ReplaceDoc(ctx, db, coll, id, text)
}

func (s *MongoService) DeleteDocs(ctx context.Context, connID, db, coll string, ids []mdb.Val) (int64, error) {
	c, err := s.client(connID)
	if err != nil {
		return 0, err
	}
	return c.DeleteDocs(ctx, db, coll, ids)
}

func (s *MongoService) EditField(ctx context.Context, connID, db, coll string, e mdb.FieldEdit) error {
	c, err := s.client(connID)
	if err != nil {
		return err
	}
	return c.EditField(ctx, db, coll, e)
}

// ParseValue converts text to a value of BSON type t (the grid's type-preserving cell edit).
// With t "shell" the text is a shell expression such as ISODate("...") or { a: 1 }.
func (s *MongoService) ParseValue(t, text string) (mdb.Val, error) {
	if t == "shell" {
		v, err := mdb.EvalShell(text)
		if err != nil {
			return mdb.Val{}, err
		}
		return mdb.ToVal(v), nil
	}
	v, err := mdb.ParseScalar(t, text, "")
	if err != nil {
		return mdb.Val{}, err
	}
	return mdb.ToVal(v), nil
}

// SampleFields lists dotted field paths seen in a sample of a collection, for completion.
func (s *MongoService) SampleFields(ctx context.Context, connID, db, coll string) ([]mdb.FieldInfo, error) {
	c, err := s.client(connID)
	if err != nil {
		return nil, err
	}
	return c.SampleFields(ctx, db, coll, 100)
}

// ---- shells ------------------------------------------------------------------------------

func (s *MongoService) OpenShell(connID, db string) (string, error) {
	return s.M.OpenMongoShell(connID, db)
}

func (s *MongoService) Run(ctx context.Context, shellID, script string, pageSize int) (mdb.RunOutput, error) {
	return s.M.RunMongo(ctx, shellID, script, pageSize)
}

func (s *MongoService) Stop(shellID string) { s.M.StopMongo(shellID) }

// FetchResult is a further page of a cursor result.
type FetchResult struct {
	Page    mdb.Page `json:"page"`
	HasMore bool     `json:"hasMore"`
}

func (s *MongoService) Fetch(ctx context.Context, shellID, cursorID string, skip, limit int) (FetchResult, error) {
	p, more, err := s.M.FetchMongo(ctx, shellID, cursorID, skip, limit)
	return FetchResult{Page: p, HasMore: more}, err
}

func (s *MongoService) ShellDB(shellID string) string { return s.M.MongoShellDB(shellID) }

func (s *MongoService) CloseShell(shellID string) { s.M.CloseMongoShell(shellID) }

// Format renders a value in shell syntax (JSON-style keys when json is set).
func (s *MongoService) Format(v mdb.Val, indent string, json bool) string {
	return mdb.Format(v, indent, json)
}

// ---- import / export ------------------------------------------------------------------------

// Export writes a collection to path as a background job.
func (s *MongoService) Export(connID, db, coll string, o mdb.ExportOptions, path string) (core.Job, error) {
	return s.M.MongoExport(connID, db, coll, o, path)
}

// Import reads path into a collection as a background job.
func (s *MongoService) Import(connID, db, coll string, o mdb.ImportOptions, path string) (core.Job, error) {
	return s.M.MongoImport(connID, db, coll, o, path)
}
