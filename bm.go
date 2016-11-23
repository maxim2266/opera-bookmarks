/*
Copyright (c) 2016, Maxim Konakov
All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice,
   this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.
3. Neither the name of the copyright holder nor the names of its contributors
   may be used to endorse or promote products derived from this software without
   specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT,
INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING,
BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY
OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE,
EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juju/gnuflag"
)

func main() {
	// command line parameters
	inputName, outputName := parseCmdLine()

	// read raw data
	data, err := loadRawData(inputName)

	if err != nil {
		die(err)
	}

	// create root folder
	var root *Folder

	if root, err = buildTree("roots", data); err != nil {
		die(err)
	}

	// printout
	//printFolder(root, 0)
	if err = writeFolders(outputName, root.Folders); err != nil {
		die(err)
	}
}

// command line parameters processor
const stdout = "STDOUT"

func parseCmdLine() (string, string) {
	defaultInput := filepath.Join(os.Getenv("HOME"), ".config", "opera", "Bookmarks")

	// parse
	var inputName string

	gnuflag.StringVar(&inputName, "input", defaultInput, "Bookmarks file pathname")
	gnuflag.StringVar(&inputName, "i", defaultInput, "Bookmarks file pathname")

	var outputName string

	gnuflag.StringVar(&outputName, "output", stdout, "Output file pathname")
	gnuflag.StringVar(&outputName, "o", stdout, "Output file pathname")
	gnuflag.Parse(false)

	return inputName, outputName
}

// writes folders as html
func writeFolders(name string, folders []*Folder) error {
	return withWriter(name)(func(out StringWriter) error {
		return foldersToHTML(folders, out)
	})
}

// string writer
// it's surprising there is no such interface in the standard library
type StringWriter interface {
	WriteString(string) (int, error)
}

// function writing to the supplied StringWriter instance
type WriterFunc func(StringWriter) error

// makes a wrapper function for the output writer
func withWriter(name string) func(WriterFunc) error {
	if name == stdout {
		return func(fn WriterFunc) error {
			w := bufio.NewWriter(os.Stdout)

			if err := fn(w); err != nil {
				return err
			}

			return w.Flush()
		}
	}

	return func(fn WriterFunc) (err error) {
		var file *os.File

		if file, err = os.Create(name); err != nil {
			return
		}

		defer func() {
			if e := file.Close(); e != nil && err == nil {
				err = e
			}

			if err != nil {
				os.Remove(name)
			}
		}()

		err = fn(file)
		return
	}
}

// read raw json data from file
func loadRawData(name string) (interface{}, error) {
	file, err := os.Open(name)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	var top struct {
		Roots interface{}
	}

	if err = json.NewDecoder(file).Decode(&top); err != nil {
		return nil, err
	}

	return top.Roots, nil
}

// build bookmarks tree
func buildTree(key string, item interface{}) (*Folder, error) {
	var node map[string]interface{}
	var ok bool

	// check node type
	if node, ok = item.(map[string]interface{}); !ok {
		return nil, errors.New("Invalid root item type")
	}

	return makeRootFolder(key, node)
}

// common data for every node
type Node struct {
	Name, Key       string
	Added, Modified time.Time
}

// node reader
func (node *Node) read(key string, data map[string]interface{}) (err error) {
	// key
	node.Key = key

	// name
	if node.Name, err = readString("name", data); err != nil {
		return
	}

	// time added
	if node.Added, err = readTimeStamp("date_added", data); err != nil {
		return
	}

	// time modified
	if node.Modified, err = readTimeStamp("date_modified", data); err != nil {
		if _, ok := err.(KeyNotFoundError); ok {
			err = nil // ignore error if the key is not found
		}
	}

	// all done
	return
}

// "url" node
type Link struct {
	Node
	URL string
}

func makeLink(key string, node map[string]interface{}) (*Link, error) {
	link := new(Link)
	err := link.Node.read(key, node)

	if err != nil {
		return nil, mapError(key, err)
	}

	if link.URL, err = readString("url", node); err != nil {
		return nil, mapError(key, err)
	}

	return link, nil
}

// "folder" node
type Folder struct {
	Node
	Links   []*Link
	Folders []*Folder
}

// Folder constructor from an element from "children" list
func makeChildFolder(key string, node map[string]interface{}) (*Folder, error) {
	// read folder header
	folder := new(Folder)

	if err := folder.Node.read(key, node); err != nil {
		return nil, mapError(key, err)
	}

	// read children
	if c, ok := node["children"]; ok {
		var cc []interface{}

		if cc, ok = c.([]interface{}); !ok {
			return nil, &ParserError{key, "Unexpected \"children\" type"}
		}

		for i, v := range cc {
			if err := folder.add("#"+strconv.Itoa(i), v); err != nil {
				return nil, mapError(key, err)
			}
		}
	}

	return folder, nil
}

// root Folder constructor
func makeRootFolder(key string, node map[string]interface{}) (*Folder, error) {
	// create folder
	folder := &Folder{
		Node: Node{
			Name: key,
			Key:  key,
		},
	}

	// read the folder
	for k, v := range node {
		if err := folder.add(k, v); err != nil {
			return nil, mapError(key, err)
		}
	}

	return folder, nil
}

// item dispatcher
func (root *Folder) add(key string, item interface{}) error {
	// check node type
	node, ok := item.(map[string]interface{})

	if !ok {
		return &ParserError{key, "Unexpected node type"}
	}

	if t, ok := node["type"]; ok { // child node
		tt, ok := t.(string)

		if !ok {
			return &ParserError{key, "Type tag is not a string"}
		}

		// dispatch on node type
		switch tt {
		case "folder":
			return root.addFolder(makeChildFolder(key, node))
		case "url":
			return root.addLink(makeLink(key, node))
		default:
			return &ParserError{key, fmt.Sprintf("Unknown type %q", tt)}
		}
	}

	// root folder node
	return root.addFolder(makeRootFolder(key, node))
}

// adders
func (folder *Folder) addFolder(child *Folder, err error) error {
	if err == nil {
		folder.Folders = append(folder.Folders, child)
	}

	return err
}

func (folder *Folder) addLink(child *Link, err error) error {
	if err == nil {
		folder.Links = append(folder.Links, child)
	}

	return err
}

// data conversion error
type KeyNotFoundError struct {
	key string
}

func (e KeyNotFoundError) Error() string {
	return fmt.Sprintf("Tag %q is not found", e.key)
}

// parser error
type ParserError struct {
	path, msg string
}

func (e *ParserError) Error() string {
	return "Node " + e.path + ": " + e.msg
}

func mapError(key string, err error) error {
	if e, ok := err.(*ParserError); ok {
		if len(e.path) > 0 {
			e.path = key + "/" + e.path
		} else {
			e.path = key
		}

		return e
	}

	return &ParserError{
		path: key,
		msg:  err.Error(),
	}
}

// data converters
func readString(key string, data map[string]interface{}) (s string, err error) {
	var ok bool
	var v interface{}

	if v, ok = data[key]; !ok {
		err = KeyNotFoundError{key}
	} else if s, ok = v.(string); !ok {
		err = fmt.Errorf("Tag %q is not a string", key)
	}

	return
}

func readInt(key string, data map[string]interface{}, bitSize int) (val int64, err error) {
	var s string

	if s, err = readString(key, data); err == nil {
		if val, err = strconv.ParseInt(s, 10, bitSize); err != nil {
			err = fmt.Errorf("Value for tag %q is not an integer: %q", key, s)
		}
	}

	return
}

var googleEpoch = time.Date(1601, time.January, 1, 0, 0, 0, 0, time.UTC)

func readTimeStamp(key string, data map[string]interface{}) (ts time.Time, err error) {
	var val int64

	if val, err = readInt(key, data, 64); err == nil {
		// Google timestamp is the number of microseconds since 01/01/1601 00:00.00
		// https://stackoverflow.com/questions/37196584/correctly-converting-chrome-timestamp-to-date-using-python
		ts = googleEpoch
		// max duration is about 290 years so have to run the loop here:
		const twoCenturies = 200 * 365 * 24 * 60 * 60 * 1000000 // microseconds

		for ; val >= twoCenturies; val -= twoCenturies {
			ts = ts.Add(time.Duration(twoCenturies * 1000)) // in nanoseconds
		}

		ts = ts.Add(time.Duration(val * 1000))
	}

	return
}

// HTML generator
type fhtml func(StringWriter) error

func htmlNil(_ StringWriter) error {
	return nil
}

func htmlRawText(text string) fhtml {
	return func(dest StringWriter) (err error) {
		_, err = dest.WriteString(text)
		return
	}
}

func htmlText(text string) fhtml {
	return htmlRawText(html.EscapeString(text))
}

func htmlTag(tag string, fn fhtml) fhtml {
	return htmlListArgs(htmlRawText("<"+tag+">"), fn, htmlRawText("</"+tag+">"))
}

func htmlLink(link, text string) fhtml {
	return htmlRawText(fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(link), html.EscapeString(text)))
}

func htmlList(fns []fhtml) fhtml {
	return func(dest StringWriter) (err error) {
		for _, f := range fns {
			if err = f(dest); err != nil {
				break
			}
		}

		return
	}
}

func htmlListArgs(fns ...fhtml) fhtml {
	return htmlList(fns)
}

func folderName(folder *Folder) fhtml {
	return htmlTag("h4", htmlText(folder.Name))
}

func folderLinks(folder *Folder) fhtml {
	if len(folder.Links) == 0 {
		return htmlNil
	}

	fns := make([]fhtml, len(folder.Links))

	for i, lnk := range folder.Links {
		fns[i] = htmlTag("dt", htmlLink(lnk.URL, lnk.Name))
	}

	return htmlTag("dl", htmlList(fns))
}

func folderList(folders []*Folder) fhtml {
	if len(folders) == 0 {
		return htmlNil
	}

	fns := make([]fhtml, len(folders))

	for i, folder := range folders {
		fns[i] = htmlTag("li", htmlListArgs(
			folderName(folder),
			folderLinks(folder),
			folderList(folder.Folders),
		))
	}

	return htmlTag("ul", htmlList(fns))
}

const htmlHeader = `<!DOCTYPE HTML><html>
<head>
<meta charset="utf-8"/><title>Bookmarks</title><style> ul { list-style-type: disc; } </style>
</head>
`

func foldersToHTML(folders []*Folder, dest StringWriter) error {
	f := htmlListArgs(
		htmlRawText(htmlHeader),
		htmlTag("body", folderList(folders)),
		htmlRawText("</html>\n"),
	)

	return f(dest)
}

// helpers
func die(err error) {
	os.Stderr.WriteString("ERROR: " + err.Error() + "\n")
	os.Exit(1)
}

// debug printout
func printFolder(folder *Folder, level int) {
	fmt.Printf("%s(%d) Folder[%q]: %q\n",
		strings.Repeat(" ", level), level, folder.Key, folder.Name)

	level++

	for _, link := range folder.Links {
		fmt.Printf("%s(%d) Link[%q]: %q (added %s)\n",
			strings.Repeat(" ", level), level, link.Key, link.Name, link.Added)
	}

	for _, f := range folder.Folders {
		printFolder(f, level)
	}
}
