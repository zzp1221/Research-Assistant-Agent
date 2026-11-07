package main

import (
	"context"
	"fmt"
	"os"

	"research-assistant-backend/internal/documents"
)

func main() {
	root := os.Args[1]
	owner := os.Args[2]
	id := os.Args[3]
	store := documents.NewStore(root)
	doc, err := store.GetDocument(context.Background(), owner, id)
	fmt.Println("err", err)
	if doc != nil {
		fmt.Println("pages", len(doc.Pages), "status", doc.Status)
		sections := documents.Outline(doc)
		fmt.Println("sections", len(sections))
		for i, s := range sections {
			fmt.Printf("%d %s %d %d\n", i, s.Title, s.StartPage, s.EndPage)
		}
		refs := documents.ExtractReferences(doc)
		fmt.Println("refs", len(refs))
		for i, r := range refs {
			if i < 5 {
				fmt.Println("REF", i, "page", r.Page, "title", r.Title, "doi", r.DOI, "arxiv", r.ArxivID)
			}
		}
	}
}
