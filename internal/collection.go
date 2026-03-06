package internal

import (
	"encoding/json"
	"fmt"

	jsondb "github.com/pnkj-kmr/simple-json-db"
)

type Collection struct {
	db         *DB
	collection jsondb.Collection
	name       string
}

func NewCollection(db *DB, name string) *Collection {
	col, err := db.GetDB().Collection(name)
	if err != nil {
		panic(err)
	}
	return &Collection{
		db:         db,
		collection: col,
		name:       name,
	}
}

func (c *Collection) Name() string {
	return c.name
}

func (c *Collection) DB() jsondb.DB {
	return c.db.GetDB()
}

func (c *Collection) Collection() jsondb.Collection {
	return c.collection
}

func (c *Collection) String() string {
	return fmt.Sprintf("Collection: %s", c.name)
}

func (c *Collection) Set(id string, value interface{}) error {
	v, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.collection.Create(id, v)
}

func (c *Collection) Get(id string, value interface{}) error {
	v, err := c.collection.Get(id)
	if err != nil {
		return err
	}

	return json.Unmarshal(v, value)
}

func (c *Collection) Delete(id string) error {
	return c.collection.Delete(id)
}

func (c *Collection) Exists(id string) bool {
	_, err := c.collection.Get(id)
	return err == nil
}

func (c *Collection) GetAll() map[string][]byte {
	return c.collection.GetAllByName()
}

// Update updates an existing item by deleting and recreating it
func (c *Collection) Update(id string, value interface{}) error {
	v, err := json.Marshal(value)
	if err != nil {
		return err
	}

	// Delete first, then create - this is the pattern used by simple-json-db
	// for updates since Create might fail if item exists
	if err := c.collection.Delete(id); err != nil {
		return err
	}

	return c.collection.Create(id, v)
}
