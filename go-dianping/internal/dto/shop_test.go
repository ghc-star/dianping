package dto

import (
	"encoding/json"
	"github.com/learning/go-dianping/internal/model"
	"testing"
)

func TestShopPatchPreservesOmittedFieldsAndAppliesZeroValues(t *testing.T) {
	var in ShopInput
	if err := json.Unmarshal([]byte(`{"x":0,"score":0,"images":""}`), &in); err != nil {
		t.Fatal(err)
	}
	shop := model.Shop{ID: 42, Name: "existing", TypeID: 3, X: 120, Y: 30, Score: 50, Images: "old"}
	in.ApplyTo(&shop)
	if !in.HasChanges() || shop.X != 0 || shop.Score != 0 || shop.Images != "" {
		t.Fatalf("zero values lost: %+v", shop)
	}
	if shop.ID != 42 || shop.Name != "existing" || shop.TypeID != 3 || shop.Y != 30 {
		t.Fatalf("omitted fields changed: %+v", shop)
	}
	if (ShopInput{}).HasChanges() {
		t.Fatal("empty patch accepted")
	}
}
