package internal

import jsondb "github.com/pnkj-kmr/simple-json-db"

type DB struct {
	db jsondb.DB
}

func NewDB(dbPath string) (*DB, error) {
	db, err := jsondb.New(dbPath, &jsondb.Options{UseGzip: false})
	if err != nil {
		return nil, err
	}

	return &DB{
		db: db,
	}, nil
}

func (d *DB) GetDB() jsondb.DB {
	return d.db
}
