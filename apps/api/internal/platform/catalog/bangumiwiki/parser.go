package bangumiwiki

import "fmt"

type Infobox struct {
	Type   string
	Fields []Field
}

type Field struct {
	Key   string
	Value string
	Items []Item
	Array bool
	Null  bool
}

type Item struct {
	Key   string
	Value string
}

func Parse(infobox string) (box Infobox, err error) {
	defer func() {
		if r := recover(); r != nil {
			box = Infobox{}
			err = fmt.Errorf("bangumiwiki: parser panic: %v", r)
		}
	}()

	box, perr := parseInfobox(infobox)
	if perr != nil {
		return Infobox{}, fmt.Errorf("bangumiwiki: %w", perr)
	}
	return box, nil
}
