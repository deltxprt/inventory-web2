package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"
	"net/http"
)

type Server struct {
	ID   string   `json:"id"`
	FQDN string   `json:"fqdn"`
	IP   string   `json:"ip"`
	Tags []string `json:"tags"`
}

type application struct {
	db *bolt.DB
}

func validateServer(v *Validator, server *Server) {
	v.Check(server.IP != "", "ip", "must be provided")
	v.Check(server.FQDN != "", "fqdn", "must be provided")
	v.Check(len(server.Tags) > 0, "tags", "must be provided")
	v.Check(Matches(server.IP, IpRX), "ip", "must be a valid IP address")
}

func validateIp(v *Validator, server *Server) {
	v.Check(server.IP != "", "ip", "must be provided")
	v.Check(Matches(server.IP, IpRX), "ip", "must be a valid IP address")
}

func (app *application) getServer(c *gin.Context) {
	var server *Server
	id := c.Param("id")
	err := app.db.View(func(tx *bolt.Tx) error {
		serverInfoByte := tx.Bucket([]byte("DB")).Bucket([]byte("INV")).Get([]byte(id))
		if serverInfoByte == nil {
			return nil
		}

		buffer := bytes.NewBuffer(serverInfoByte)
		decoder := gob.NewDecoder(buffer)
		err := decoder.Decode(&server)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return
	}
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": server})
}

func (app *application) getInventory(c *gin.Context) {
	var servers []*Server
	err := app.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("DB")).Bucket([]byte("INV"))
		if bucket == nil {
			return fmt.Errorf("bucket not found")
		}
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var server Server
			buffer := bytes.NewBuffer(v)
			decoder := gob.NewDecoder(buffer)
			err := decoder.Decode(&server)
			if err != nil {
				return err
			}

			servers = append(servers, &server)
		}

		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if servers == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "the inventory is empty"})
		return
	}

	params := c.Request.URL.Query()

	formatSet := params.Get("format")

	if formatSet == "yaml" {

		c.YAML(http.StatusOK, convertToAnsibleInventory(servers))
		return
	} else {
		c.JSON(http.StatusOK, gin.H{"data": servers})
		return
	}

}

func (app *application) addServer(c *gin.Context) {
	var server *Server
	err := c.BindJSON(&server)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	v := NewValidator()
	if validateServer(v, server); !v.Valid() {

		c.JSON(http.StatusConflict, gin.H{"error": v.Errors})
		return
	}

	server.ID = uuid.New().String()
	err = app.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("DB")).Bucket([]byte("INV"))
		if bucket == nil {
			return nil
		}

		buffer := new(bytes.Buffer)
		decoder := gob.NewEncoder(buffer)
		err := decoder.Encode(&server)
		if err != nil {
			return err
		}

		err = bucket.Put([]byte(server.ID), buffer.Bytes())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": server})
}

func (app *application) updateServer(c *gin.Context) {
	serverId := c.Param("id")
	var server *Server
	var currentInfo *Server
	err := c.BindJSON(&server)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	v := NewValidator()
	if server.IP != "" {
		if validateIp(v, server); !v.Valid() {
			c.JSON(http.StatusConflict, gin.H{"error": v.Errors})
			return
		}
	}

	err = app.db.View(func(tx *bolt.Tx) error {
		serverInfoByte := tx.Bucket([]byte("DB")).Bucket([]byte("INV")).Get([]byte(serverId))
		if serverInfoByte == nil {
			return nil
		}

		buffer := bytes.NewBuffer(serverInfoByte)
		decoder := gob.NewDecoder(buffer)
		err := decoder.Decode(&currentInfo)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	if currentInfo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}

	if server.IP != currentInfo.IP && server.IP != "" {
		currentInfo.IP = server.IP
	}

	if server.FQDN != currentInfo.FQDN && server.FQDN != "" {
		currentInfo.FQDN = server.FQDN
	}

	if len(server.Tags) != len(currentInfo.Tags) && len(server.Tags) > 0 {
		currentInfo.Tags = server.Tags
	}

	err = app.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("DB")).Bucket([]byte("INV"))
		if bucket == nil {
			return nil
		}

		buffer := new(bytes.Buffer)
		decoder := gob.NewEncoder(buffer)
		err := decoder.Encode(&currentInfo)
		if err != nil {
			return err
		}

		err = bucket.Put([]byte(currentInfo.ID), buffer.Bytes())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": currentInfo})
}

func (app *application) deleteServer(c *gin.Context) {
	serverId := c.Param("id")

	err := app.db.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket([]byte("DB")).Bucket([]byte("INV")).Delete([]byte(serverId))
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, nil)
}

func convertToAnsibleInventory(servers []*Server) map[string]interface{} {
	inventory := make(map[string]interface{})
	groups := make(map[string]map[string]map[string]string)

	for _, server := range servers {
		for _, tag := range server.Tags {
			if _, exists := groups[tag]; !exists {
				groups[tag] = make(map[string]map[string]string)
			}
			groups[tag][server.FQDN] = map[string]string{
				"ansible_host": server.IP,
			}
		}
	}

	for group, hosts := range groups {
		inventory[group] = map[string]interface{}{
			"hosts": hosts,
		}
	}

	return inventory
}

func main() {

	db, err := openDB()
	if err != nil {
		return
	}
	app := &application{
		db: db,
	}
	r := gin.Default()
	r.GET("/inventory", app.getInventory)
	r.GET("/inventory/:id", app.getServer)
	r.POST("/inventory", app.addServer)
	r.PUT("/inventory/:id", app.updateServer)
	r.DELETE("/inventory/:id", app.deleteServer)
	err = r.Run("127.0.0.1:8080")
	if err != nil {
		return
	}
}

func openDB() (*bolt.DB, error) {
	db, err := bolt.Open("main.db", 0600, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		root, err := tx.CreateBucketIfNotExists([]byte("DB"))
		if err != nil {
			return fmt.Errorf("could not create root bucket: %v", err)
		}

		_, err = root.CreateBucketIfNotExists([]byte("INV"))
		if err != nil {
			return fmt.Errorf("could not create certificates bucket: %v", err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("could not set up buckets, %v", err)
	}

	return db, nil
}
