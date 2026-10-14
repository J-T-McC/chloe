package mdb

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestZZTypes(t *testing.T) {
	v, _ := EvalShell("{a: 5.0}")
	fmt.Printf("%T\n", v.(bson.D)[0].Value)
}

func TestZZIT(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	sh := c.NewShell("shop")
	show := func(script string, page int) RunOutput {
		o := sh.Run(ctx, script, page)
		fmt.Printf("SCRIPT %s\n  err=%q\n", script, o.Error)
		for _, r := range o.Results {
			n := -1
			if r.Page != nil {
				n = len(r.Page.Docs)
			}
			v := ""
			if r.Value != nil {
				v = Format(*r.Value, "", false)
			}
			fmt.Printf("  stmt=%q kind=%s docs=%d more=%v val=%s text=%q\n", r.Statement, r.Kind, n, r.HasMore, v, r.Text)
		}
		return o
	}
	o := show("db.orders.find().sort({_id:1}).skip(5).limit(25)", 20)
	p, more, err := sh.Fetch(ctx, o.Results[0].CursorID, 20, 20)
	fmt.Println("page2", len(p.Docs), more, err)
	o = show("db.orders.find({}, {}, {limit: 25, skip: 5})", 20)
	p, more, err = sh.Fetch(ctx, o.Results[0].CursorID, 20, 20)
	fmt.Println("page2 opts", len(p.Docs), more, err)
	show("db.orders.find().limit(-3)", 20)
	show("db.orders.find().limit(0)", 20)
	show("db.orders.aggregate([{$match:{}}]).limit(2)", 20)
	show("db.orders.aggregate([{$match:{}}]).skip(55)", 20)
	show("db.zz_tmp.drop(); if (true) { db.orders.aggregate([{$match:{}}, {$out: 'zz_tmp'}]) }; db.zz_tmp.countDocuments()", 20)
	show("var r = db.orders.aggregate([{$match:{}}, {$out: 'zz_tmp2'}]); db.zz_tmp2.countDocuments()", 20)
	show("db.orders.aggregate([{$match:{}}, {$out: 'zz_tmp3'}]).sort({_id:1})", 20)
	show("db.zz_tmp.drop(); db.zz_tmp2.drop(); db.zz_tmp3.drop()", 20)
	show("(function(){ return 1 })(); (2)+1; 'x'", 20)
	show("db.orders.find().map(d => d.status)", 5)
	show("db.orders.findOne({}, {_id:1}).total", 5)
	show("db.orders.find().count()", 5)
}
