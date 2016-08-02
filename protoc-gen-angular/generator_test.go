package main

import (
	"testing"
)

func TestToJsonName(t *testing.T) {
	tests := []struct {
		str  string
		want string
	}{
		{
			str:"",
			want:"",
		}, {
			str:"id",
			want:"id",
		}, {
			str:"material_id",
			want:"materialId",
		}, {
			str:"material_id_list",
			want:"materialIdList",
		}, {
			str:"materialIdList",
			want:"materialIdList",
		}, {
			str:"MaterialIdList",
			want:"materialIdList",
		}, {
			str:"MaterialId_list",
			want:"materialIdList",
		},
	}
	for i, test := range tests {
		got := ToJsonName(test.str)
		if test.want != got {
			t.Fatalf("case %d: input='%s' want='%s' got='%s'", i, test.str, test.want, got)
		}
	}
}