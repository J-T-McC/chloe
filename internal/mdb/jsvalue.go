package mdb

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// prelude defines the BSON wrapper types of the shell. Every wrapper carries a
// _bsontype tag on its prototype, which toBSON reads to convert it.
const prelude = `
(function (g) {
  function def(name, tag, init, proto) {
    var F = function () {
      if (!(this instanceof F)) { var o = Object.create(F.prototype); init.apply(o, arguments); return o; }
      init.apply(this, arguments);
    };
    Object.defineProperty(F, "name", { value: name });
    Object.defineProperty(F.prototype, "_bsontype", { value: tag });
    for (var k in proto) Object.defineProperty(F.prototype, k, { value: proto[k], writable: true });
    g[name] = F;
    return F;
  }
  var OID = def("ObjectId", "ObjectId", function (id) {
    this.$oid = id === undefined || id === null ? __oid() : __checkOid(id instanceof OID ? id.$oid : String(id));
  }, {
    toString: function () { return this.$oid; },
    toHexString: function () { return this.$oid; },
    valueOf: function () { return this.$oid; },
    getTimestamp: function () { return new Date(parseInt(this.$oid.slice(0, 8), 16) * 1000); },
    equals: function (o) { return o != null && String(o.$oid || o) === this.$oid; },
    toJSON: function () { return { $oid: this.$oid }; },
    inspect: function () { return 'ObjectId("' + this.$oid + '")'; },
  });
  Object.defineProperty(OID.prototype, "str", { get: function () { return this.$oid; } });
  OID.createFromTime = function (s) { return OID(__oidFromTime(Number(s))); };
  OID.isValid = function (s) { try { __checkOid(String(s)); return true; } catch (e) { return false; } };
  OID.createFromHexString = function (s) { return OID(s); };

  var L = def("NumberLong", "Long", function (v) { this.$v = __checkLong(v === undefined ? "0" : String(v)); }, {
    toString: function () { return this.$v; },
    valueOf: function () { return Number(this.$v); },
    toNumber: function () { return Number(this.$v); },
    toJSON: function () { return { $numberLong: this.$v }; },
  });
  g.Long = L;
  L.fromNumber = function (n) { return L(Math.trunc(n)); };
  L.fromString = function (s) { return L(s); };
  def("NumberInt", "Int32", function (v) { this.$v = Number(v || 0) | 0; }, {
    toString: function () { return String(this.$v); },
    valueOf: function () { return this.$v; },
    toJSON: function () { return this.$v; },
  });
  g.Int32 = g.NumberInt;
  def("Double", "Double", function (v) { this.$v = v === undefined ? 0 : Number(v); }, {
    toString: function () { return String(this.$v); },
    valueOf: function () { return this.$v; },
    toJSON: function () { return this.$v; },
  });
  def("NumberDecimal", "Decimal128", function (v) { this.$v = __checkDecimal(v === undefined ? "0" : String(v)); }, {
    toString: function () { return this.$v; },
    valueOf: function () { return Number(this.$v); },
    toJSON: function () { return { $numberDecimal: this.$v }; },
  });
  g.Decimal128 = g.NumberDecimal;
  g.Decimal128.fromString = function (s) { return g.NumberDecimal(s); };
  var Bin = def("BinData", "Binary", function (sub, b64) { this.$sub = Number(sub) | 0; this.$b64 = __checkBase64(String(b64 || "")); }, {
    toString: function () { return this.$sub === 4 ? __b64ToUUID(this.$b64) : this.$b64; },
    base64: function () { return this.$b64; },
    hex: function () { return __b64ToHex(this.$b64); },
    subtype: function () { return this.$sub; },
    length: function () { return __b64Len(this.$b64); },
    toJSON: function () { return { $binary: { base64: this.$b64, subType: this.$sub.toString(16) } }; },
  });
  g.Binary = Bin;
  g.UUID = function (s) { return Bin(4, __uuidToB64(s === undefined ? "" : String(s))); };
  g.HexData = function (sub, h) { return Bin(sub, __hexToB64(String(h))); };
  g.MD5 = function (h) { return Bin(5, __hexToB64(String(h))); };
  def("Timestamp", "Timestamp", function (t, i) {
    if (t !== null && typeof t === "object") { i = t.i; t = t.t; }
    this.$t = Number(t || 0) >>> 0; this.$i = Number(i || 0) >>> 0;
  }, {
    toString: function () { return "Timestamp(" + this.$t + ", " + this.$i + ")"; },
    getTime: function () { return this.$t; },
    getInc: function () { return this.$i; },
    toJSON: function () { return { $timestamp: { t: this.$t, i: this.$i } }; },
  });
  def("MinKey", "MinKey", function () {}, { toJSON: function () { return { $minKey: 1 }; } });
  def("MaxKey", "MaxKey", function () {}, { toJSON: function () { return { $maxKey: 1 }; } });
  def("BSONRegExp", "BSONRegExp", function (p, o) { this.pattern = String(p); this.options = String(o || ""); }, {
    toString: function () { return "/" + this.pattern + "/" + this.options; },
  });
  def("Code", "Code", function (c) { this.code = String(c); }, { toString: function () { return this.code; } });
  g.ISODate = function (s) { return s === undefined ? new Date() : new Date(__isoDate(String(s))); };
})(this);
`

// toJS converts a decoded BSON value to a shell value.
func (s *Shell) toJS(v any) goja.Value {
	vm := s.vm
	switch x := v.(type) {
	case nil, bson.Null:
		return goja.Null()
	case bson.D:
		o := vm.NewObject()
		for _, e := range x {
			_ = o.Set(e.Key, s.toJS(e.Value))
		}
		return o
	case bson.M:
		o := vm.NewObject()
		for k, e := range x {
			_ = o.Set(k, s.toJS(e))
		}
		return o
	case bson.A:
		items := make([]any, len(x))
		for i, e := range x {
			items[i] = s.toJS(e)
		}
		return vm.NewArray(items...)
	case bson.Raw:
		var d bson.D
		if err := bson.Unmarshal(x, &d); err != nil {
			panic(vm.NewGoError(err))
		}
		return s.toJS(d)
	case string, bool, float64:
		return vm.ToValue(x)
	case int32:
		return vm.ToValue(int64(x))
	case int:
		return vm.ToValue(int64(x))
	case int64:
		return s.newWrapper("NumberLong", strconv.FormatInt(x, 10))
	case bson.ObjectID:
		return s.newWrapper("ObjectId", x.Hex())
	case bson.DateTime:
		return s.date(int64(x))
	case time.Time:
		return s.date(x.UnixMilli())
	case bson.Decimal128:
		return s.newWrapper("NumberDecimal", x.String())
	case bson.Binary:
		return s.newWrapper("BinData", int64(x.Subtype), base64.StdEncoding.EncodeToString(x.Data))
	case bson.Regex:
		if jsFlags(x.Options) {
			r, err := vm.New(vm.Get("RegExp"), vm.ToValue(x.Pattern), vm.ToValue(x.Options))
			if err == nil {
				return r
			}
		}
		return s.newWrapper("BSONRegExp", x.Pattern, x.Options)
	case bson.Timestamp:
		return s.newWrapper("Timestamp", int64(x.T), int64(x.I))
	case bson.MinKey:
		return s.newWrapper("MinKey")
	case bson.MaxKey:
		return s.newWrapper("MaxKey")
	case bson.Undefined:
		return goja.Undefined()
	case bson.JavaScript:
		return s.newWrapper("Code", string(x))
	case bson.CodeWithScope:
		return s.newWrapper("Code", string(x.Code))
	case bson.Symbol:
		return vm.ToValue(string(x))
	case bson.DBPointer:
		return s.toJS(bson.D{{Key: "$ref", Value: x.DB}, {Key: "$id", Value: x.Pointer}})
	}
	return vm.ToValue(v)
}

func jsFlags(opts string) bool {
	for _, r := range opts {
		if !strings.ContainsRune("ims", r) {
			return false
		}
	}
	return true
}

func (s *Shell) date(ms int64) goja.Value {
	d, err := s.vm.New(s.vm.Get("Date"), s.vm.ToValue(ms))
	if err != nil {
		panic(err)
	}
	return d
}

func (s *Shell) newWrapper(name string, args ...any) goja.Value {
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = s.vm.ToValue(a)
	}
	o, err := s.vm.New(s.vm.Get(name), vals...)
	if err != nil {
		panic(err)
	}
	return o
}

// toBSON converts a shell value to BSON. Integral numbers in the int32 range
// become int32 and other numbers doubles, as in mongosh.
func (s *Shell) toBSON(v goja.Value) (any, error) { return s.toBSONDepth(v, 0) }

func (s *Shell) toBSONDepth(v goja.Value, depth int) (any, error) {
	if depth > 150 {
		return nil, fmt.Errorf("value is nested too deeply (or refers to itself)")
	}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	o, ok := v.(*goja.Object)
	if !ok {
		switch x := v.Export().(type) {
		case int64:
			if x >= math.MinInt32 && x <= math.MaxInt32 {
				return int32(x), nil
			}
			return float64(x), nil
		case float64:
			if x == math.Trunc(x) && x >= math.MinInt32 && x <= math.MaxInt32 && !(x == 0 && math.Signbit(x)) {
				return int32(x), nil
			}
			return x, nil
		case string, bool:
			return x, nil
		}
		if b, ok := v.Export().(*big.Int); ok { // BigInt
			if !b.IsInt64() {
				return nil, fmt.Errorf("%s is too large for a 64-bit integer", b)
			}
			return b.Int64(), nil
		}
		return v.String(), nil
	}
	switch o.ClassName() {
	case "Date":
		if t, ok := o.Export().(time.Time); ok {
			return bson.NewDateTimeFromTime(t), nil
		}
		return nil, fmt.Errorf("invalid date")
	case "RegExp":
		flags := o.Get("flags").String()
		opts := ""
		for _, r := range flags {
			if strings.ContainsRune("imsux", r) {
				opts += string(r)
			}
		}
		return bson.Regex{Pattern: o.Get("source").String(), Options: opts}, nil
	case "Array":
		n := int(o.Get("length").ToInteger())
		a := make(bson.A, n)
		for i := 0; i < n; i++ {
			e, err := s.toBSONDepth(o.Get(strconv.Itoa(i)), depth+1)
			if err != nil {
				return nil, err
			}
			a[i] = e
		}
		return a, nil
	case "Function":
		return bson.JavaScript(o.String()), nil
	case "Number":
		return s.toBSONDepth(o.ToNumber(), depth)
	case "String":
		return o.String(), nil
	case "Boolean":
		b, _ := o.Export().(bool)
		return b, nil
	}
	if c, ok := o.Export().(*cursor); ok {
		return nil, fmt.Errorf("a cursor can't be stored; call toArray() on %s first", c.ns())
	}
	if tag := o.Get("_bsontype"); tag != nil && !goja.IsUndefined(tag) {
		return s.wrapperToBSON(tag.String(), o)
	}
	keys := o.Keys()
	d := make(bson.D, 0, len(keys))
	for _, k := range keys {
		ev := o.Get(k)
		if f, ok := ev.(*goja.Object); ok && f.ClassName() == "Function" {
			continue
		}
		e, err := s.toBSONDepth(ev, depth+1)
		if err != nil {
			return nil, err
		}
		d = append(d, bson.E{Key: k, Value: e})
	}
	return d, nil
}

func (s *Shell) wrapperToBSON(tag string, o *goja.Object) (any, error) {
	str := func(k string) string { return o.Get(k).String() }
	switch tag {
	case "ObjectId":
		return bson.ObjectIDFromHex(str("$oid"))
	case "Long":
		return strconv.ParseInt(str("$v"), 10, 64)
	case "Int32":
		return int32(o.Get("$v").ToInteger()), nil
	case "Double":
		return o.Get("$v").ToFloat(), nil
	case "Decimal128":
		return bson.ParseDecimal128(str("$v"))
	case "Binary":
		b, err := base64.StdEncoding.DecodeString(str("$b64"))
		return bson.Binary{Subtype: byte(o.Get("$sub").ToInteger()), Data: b}, err
	case "Timestamp":
		return bson.Timestamp{T: uint32(o.Get("$t").ToInteger()), I: uint32(o.Get("$i").ToInteger())}, nil
	case "MinKey":
		return bson.MinKey{}, nil
	case "MaxKey":
		return bson.MaxKey{}, nil
	case "BSONRegExp":
		return bson.Regex{Pattern: str("pattern"), Options: str("options")}, nil
	case "Code":
		return bson.JavaScript(str("code")), nil
	}
	return nil, fmt.Errorf("unknown BSON type %s", tag)
}

// installHelpers defines the Go functions the prelude relies on.
func (s *Shell) installHelpers() {
	vm := s.vm
	fail := func(err error) { panic(vm.NewGoError(err)) }
	set := func(name string, f func(goja.FunctionCall) goja.Value) { _ = vm.Set(name, f) }
	set("__oid", func(goja.FunctionCall) goja.Value { return vm.ToValue(bson.NewObjectID().Hex()) })
	set("__oidFromTime", func(c goja.FunctionCall) goja.Value {
		return vm.ToValue(bson.NewObjectIDFromTimestamp(time.Unix(c.Argument(0).ToInteger(), 0)).Hex())
	})
	set("__checkOid", func(c goja.FunctionCall) goja.Value {
		id, err := bson.ObjectIDFromHex(c.Argument(0).String())
		if err != nil {
			fail(fmt.Errorf("invalid ObjectId %q: expected 24 hex characters", c.Argument(0).String()))
		}
		return vm.ToValue(id.Hex())
	})
	set("__checkLong", func(c goja.FunctionCall) goja.Value {
		t := strings.TrimSpace(c.Argument(0).String())
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return vm.ToValue(strconv.FormatInt(n, 10))
		}
		// NumberLong(1.5) truncates, as mongosh does; anything out of range is an error.
		f, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(f) || f >= math.MaxInt64 || f < math.MinInt64 {
			fail(fmt.Errorf("invalid NumberLong %q: not a 64-bit integer", t))
		}
		return vm.ToValue(strconv.FormatInt(int64(f), 10))
	})
	set("__checkDecimal", func(c goja.FunctionCall) goja.Value {
		d, err := bson.ParseDecimal128(c.Argument(0).String())
		if err != nil {
			fail(fmt.Errorf("invalid NumberDecimal %q", c.Argument(0).String()))
		}
		return vm.ToValue(d.String())
	})
	set("__checkBase64", func(c goja.FunctionCall) goja.Value {
		if _, err := base64.StdEncoding.DecodeString(c.Argument(0).String()); err != nil {
			fail(fmt.Errorf("invalid base64 data"))
		}
		return c.Argument(0)
	})
	set("__b64ToHex", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(c.Argument(0).String())
		return vm.ToValue(hex.EncodeToString(b))
	})
	set("__b64Len", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(c.Argument(0).String())
		return vm.ToValue(len(b))
	})
	set("__hexToB64", func(c goja.FunctionCall) goja.Value {
		b, err := hex.DecodeString(c.Argument(0).String())
		if err != nil {
			fail(fmt.Errorf("invalid hex data"))
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(b))
	})
	set("__b64ToUUID", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(c.Argument(0).String())
		if len(b) != 16 {
			return c.Argument(0)
		}
		return vm.ToValue(formatUUID(b))
	})
	set("__uuidToB64", func(c goja.FunctionCall) goja.Value {
		t := c.Argument(0).String()
		var b []byte
		if t == "" {
			b = newUUID()
		} else {
			var err error
			if b, err = parseUUID(t); err != nil {
				fail(err)
			}
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(b))
	})
	set("__isoDate", func(c goja.FunctionCall) goja.Value {
		t, err := ParseDate(c.Argument(0).String())
		if err != nil {
			fail(err)
		}
		return vm.ToValue(t.UnixMilli())
	})
}
