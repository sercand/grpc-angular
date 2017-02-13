package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	desc "github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway/descriptor"
	gen "github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway/generator"
	"github.com/valyala/fasttemplate"
	"path"
	"reflect"
	"strconv"
)

var (
	methodString = `{{funcName}}({{input}}): Observable<{{outType}}> {
    let _headers = new Headers();
	let _query = new URLSearchParams();
    _headers.append("Content-Type", "application/json");
    for (let i = 0; i < this.headerEditors.length; ++i) {
      this.headerEditors[i].edit(_headers);
    }
    let _args = new RequestOptions({
      method: RequestMethod.{{method}},
      headers: _headers,
      search: _query,
      body: {{body}}
    });
    {{query}}
    return this.http.request({{url}}, _args)
      .map(this._extractData)
      .catch(this._handleError);
  }

   `
	tempMethod    = fasttemplate.New(methodString, "{{", "}}")
	serviceString = `@Injectable()
export class {{serviceName}}Service {
  host: string;
  headerEditors: any[];

  constructor(private http: Http) {
    this.host = "http://localhost:18870";
    this.headerEditors=[];
  }

  addHeaderEditor(m: any) {
    this.headerEditors.push(m)
  }

  private _extractData(res: Response) {
    let body = res.json();
    return body || {};
  }

  private _handleError(error: any, cauth: Observable<void>) {
    let errMsg = (error._body) ? error._body :
      (error.status ? error.status + " - " + error.statusText : 'Server error')
    return Observable.throw(errMsg);
  }
`
	tempService = fasttemplate.New(serviceString, "{{", "}}")
	queryString = `
	if([input].[field]){
		_query.append("[fieldName]", ` + "`${" + `[input].[field]` + "}`" + `)
	}`
	templateQuery = fasttemplate.New(queryString, "[", "]")
	templateEnum  = fasttemplate.New("export const {enumType}_{enumName}: {enumType} = \"{enumName}\";\n", "{", "}")
)

type packageAlias struct {
	first  string
	second string
}

type generator struct {
	reg       *descriptor.Registry
	mapValues []string
	aliases   []packageAlias
}

// New returns a new generator which generates grpc gateway files.
func NewGenerator(reg *descriptor.Registry, a []packageAlias) gen.Generator {
	return &generator{reg: reg, mapValues: []string{}, aliases: a}
}
func (g *generator) getFileName(file *descriptor.File) string {
	n := file.GetName()
	for _, a := range g.aliases {
		if a.first == n {
			return a.second
		}
	}
	return n
}

func (g *generator) importName(file *descriptor.File) string {
	fname := g.getFileName(file)
	fbase := file.GetPackage() + "_" + strings.TrimSuffix(fname, filepath.Ext(fname))
	fbase = strings.Replace(fbase, "/", "_", -1)
	fbase = strings.Replace(fbase, ".", "_", -1)
	fbase = strings.Replace(fbase, "-", "_", -1)
	return fbase
}

func (g *generator) getRawTypeName(file *descriptor.File, a string) string {
	m, err := g.reg.LookupMsg(file.GetPackage(), a)
	isMessage := false
	isEnum := false
	prefix := ""
	if err == nil {
		isMessage = true
	}
	e, err := g.reg.LookupEnum(file.GetPackage(), a)
	if err == nil {
		isEnum = true
	}
	var mf *descriptor.File
	ss := strings.Split(a, ".")
	if isMessage {
		if len(m.Outers) > 0 {
			prefix = strings.Join(m.Outers, "")
		}
		mf = m.File
	} else if isEnum {
		mf = e.File
		if len(e.Outers) > 0 {
			prefix = strings.Join(e.Outers, "")
		}
	} else {
		panic(fmt.Errorf("%s is not message or enum", a))
		return ""
	}
	if mf.GetName() == file.GetName() {
		return prefix + ss[len(ss)-1]
	} else {
		return g.importName(mf) + "." + prefix + ss[len(ss)-1]
	}
}

func (g *generator) getTypeName(t desc.FieldDescriptorProto_Type, field *desc.FieldDescriptorProto, file *descriptor.File) string {
	switch t {
	case desc.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case desc.FieldDescriptorProto_TYPE_BOOL:
		return "boolean"
	case desc.FieldDescriptorProto_TYPE_FIXED32, desc.FieldDescriptorProto_TYPE_DOUBLE:
		return "number"
	case desc.FieldDescriptorProto_TYPE_FLOAT, desc.FieldDescriptorProto_TYPE_INT32, desc.FieldDescriptorProto_TYPE_UINT32:
		return "number"
	case desc.FieldDescriptorProto_TYPE_INT64, desc.FieldDescriptorProto_TYPE_FIXED64, desc.FieldDescriptorProto_TYPE_UINT64:
		return "string|number"
	case desc.FieldDescriptorProto_TYPE_BYTES:
		return "string"
	case desc.FieldDescriptorProto_TYPE_ENUM:
		return g.getRawTypeName(file, field.GetTypeName())
	case desc.FieldDescriptorProto_TYPE_MESSAGE:
		return g.getRawTypeName(file, field.GetTypeName())
	default:
		return "any"
	}
}

func (g *generator) isMap(field *desc.FieldDescriptorProto) bool {
	if field.GetLabel() == desc.FieldDescriptorProto_LABEL_REPEATED && field.GetType() == desc.FieldDescriptorProto_TYPE_MESSAGE {
		t := field.GetTypeName()
		for _, v := range g.mapValues {
			if t == v {
				return true
			}
		}
	}
	return false
}

func (g *generator) printMessageField(w io.Writer, field *desc.FieldDescriptorProto, file *descriptor.File) {
	if g.isMap(field) {
		mapEntry, err := g.reg.LookupMsg(file.GetPackage(), field.GetTypeName())
		if err != nil {
			return
		}
		var valueField *desc.FieldDescriptorProto
		for _, ff := range mapEntry.GetField() {
			if ff.GetName() == "value" {
				valueField = ff
				break
			}
		}

		fasttemplate.New(`  {{fieldName}}: { [key: string]: {{valueType}} };
		`, "{{", "}}").Execute(w, map[string]interface{}{
			"fieldName": field.GetJsonName(),
			"valueType": g.getTypeName(valueField.GetType(), valueField, file),
		})
	} else if field.GetLabel() == desc.FieldDescriptorProto_LABEL_REPEATED {
		tn := g.getTypeName(field.GetType(), field, file)
		if strings.Index(tn, "|") > -1 {
			tn = strings.Replace(tn, "|", "[]|", 1)
		}
		fmt.Fprintf(w, "  %s: %s[];\n", field.GetJsonName(), tn)
	} else {
		fmt.Fprintf(w, "  %s: %s;\n", field.GetJsonName(), g.getTypeName(field.GetType(), field, file))
	}
}

func ToJsonName(pre string) string {
	if len(pre) == 0 {
		return ""
	}
	word := pre[:1]
	ss := make([]string, 0)
	for i := 1; i < len(pre); i++ {
		letter := pre[i : i+1]
		if word != "" && strings.ToUpper(letter) == letter {
			ss = append(ss, word)
			if letter != "_" && letter != "-" {
				word = letter
			} else {
				word = ""
			}
		} else {
			word += letter
		}
	}
	ss = append(ss, word)
	for i, v := range ss {
		if i != 0 {
			ss[i] = strings.Title(v)
		} else {
			ss[0] = strings.ToLower(ss[0])
		}
	}
	return strings.Join(ss, "")
}

func ToParamName(pre string) string {
	ss := strings.Split(pre, ".")
	return ToJsonName(ss[len(ss)-1])
}
func printComment(b io.Writer, comment string) {
	if len(comment) > 0 {
		fmt.Fprintf(b, "/**\n%s\n*/\n", comment)
	}
}
func (g *generator) generate(file *descriptor.File) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, `// Code generated by protoc-gen-angular.
// DO NOT EDIT!
`)
	if len(file.Services) > 0 {
		fmt.Fprintln(&buf, `import {Injectable} from '@angular/core';
import {Http, Response, RequestOptions, RequestMethod, Headers, URLSearchParams} from "@angular/http";
import {Observable} from "rxjs/Observable";
import 'rxjs/add/observable/throw';
import 'rxjs/add/operator/catch';`)
	}
	for _, d := range file.GetDependency() {
		if d == "google/api/annotations.proto" || d == "github.com/gogo/protobuf/gogoproto/gogo.proto" {
			continue
		}
		p := path.Base(d)
		p = strings.TrimSuffix(p, filepath.Ext(p))
		ff, fe := g.reg.LookupFile(d)
		if fe != nil {
			return "", fe
		}
		pn := g.importName(ff)
		dir := filepath.Dir(g.getFileName(ff))
		if !strings.HasPrefix(dir, ".") {
			dir = fmt.Sprintf("./%s", dir)
		}
		p = fmt.Sprintf("%s/%s.pb", dir, p)
		fmt.Fprintf(&buf, "import * as %s from \"%s\";\n", pn, p)
	}

	fmt.Fprintln(&buf, "")

	pack := file.GetPackage()
	for _, m := range file.Messages {
		mesName := fmt.Sprintf(".%s.%s", pack, m.GetName())
		for _, n := range m.GetNestedType() {
			if n.GetOptions().GetMapEntry() {
				g.mapValues = append(g.mapValues, fmt.Sprintf("%s.%s", mesName, n.GetName()))
			}
		}
	}
	valueProtoPath := protoPathIndex(reflect.TypeOf((*desc.EnumDescriptorProto)(nil)), "Value")
	for _, e := range file.Enums {
		printComment(&buf, protoComments(g.reg, e.File, e.Outers, "EnumType", int32(e.Index)))
		enumType := e.GetName()
		if len(e.Outers) > 0 {
			enumType = strings.Join(e.Outers, "") + enumType
		}
		en := fmt.Sprintf("export type %s = ", enumType)
		for i, v := range e.GetValue() {
			en = fmt.Sprintf(`%s "%s" `, en, v.GetName())
			if i != len(e.GetValue())-1 {
				en = fmt.Sprintf(`%s |`, en)
			}
		}
		fmt.Fprintf(&buf, "%s;\n", en)
		for i, v := range e.GetValue() {
			printComment(&buf, protoComments(g.reg, e.File, e.Outers, "EnumType", int32(e.Index), valueProtoPath, int32(i)))
			templateEnum.Execute(&buf, map[string]interface{}{
				"enumType": enumType,
				"enumName": v.GetName(),
			})
		}
		fmt.Fprintln(&buf, "")
	}

	for _, m := range file.Messages {
		//	glog.Errorf("message %v", m)
		if m.GetOptions().GetMapEntry() {
			continue
		}
		prefix := ""
		if len(m.Outers) > 0 {
			prefix = strings.Join(m.Outers, "")
		}
		printComment(&buf, protoComments(g.reg, m.File, m.Outers, "MessageType", int32(m.Index)))
		fmt.Fprintf(&buf, "export class %s {\n", prefix+m.GetName())
		for i, f := range m.GetField() {
			fieldProtoPath := protoPathIndex(reflect.TypeOf((*desc.DescriptorProto)(nil)), "Field")
			printComment(&buf, protoComments(g.reg, m.File, m.Outers, "MessageType", int32(m.Index), fieldProtoPath, int32(i)))
			g.printMessageField(&buf, f, file)
		}
		fmt.Fprintln(&buf, "}\n")
	}
	methProtoPath := protoPathIndex(reflect.TypeOf((*desc.ServiceDescriptorProto)(nil)), "Method")
	for svcIdx, s := range file.Services {
		printComment(&buf, protoComments(g.reg, s.File, nil, "Service", int32(svcIdx)))
		tempService.Execute(&buf, map[string]interface{}{
			"serviceName": s.GetName(),
		})
		for methIdx, m := range s.Methods {
			if len(m.Bindings) == 0 {
				continue
			}
			printComment(&buf, protoComments(g.reg, s.File, nil, "Service", int32(svcIdx), methProtoPath, int32(methIdx)))
			b := m.Bindings[0]
			method := b.HTTPMethod[:1] + strings.ToLower(b.HTTPMethod[1:])
			allFieldsUsedForUrl := false
			inMessage, err := g.reg.LookupMsg(file.GetName(), m.GetInputType())
			if err == nil {
				allFieldsUsedForUrl = len(inMessage.Fields) == len(b.PathTmpl.Fields)
			}
			inputName := ToParamName(m.GetInputType())
			body := fmt.Sprintf("%s,", inputName)
			query := ""
			if method == "Get" || method == "Delete" {
				body = "{},"
				glog.V(20).Infof("GET: %v %v", b.PathTmpl.Fields, inMessage.Fields)
				if !allFieldsUsedForUrl {
					for _, f := range inMessage.Fields {
						founded := false
						for _, pf := range b.PathTmpl.Fields {
							if pf == f.GetName() {
								founded = true
								break
							}
						}
						if !founded {
							query = query + templateQuery.ExecuteString(map[string]interface{}{
								"input":     inputName,
								"field":     ToJsonName(f.GetName()),
								"fieldName": f.GetName(),
							})
						}
					}
				}
			}
			if allFieldsUsedForUrl && (method == "Post" || method == "Put") {
				body = "{},"
			}
			inputType := fmt.Sprintf("%s: %s", inputName, g.getRawTypeName(file, m.GetInputType()))
			if allFieldsUsedForUrl && len(inMessage.Fields) > 0 {
				inputType = ""
				for _, f := range inMessage.GetField() {
					inputType = fmt.Sprintf("%s, %s: %s", inputType, f.GetJsonName(), g.getTypeName(f.GetType(), f, file))
				}
				inputType = inputType[1:]
			}
			if allFieldsUsedForUrl && len(inMessage.Fields) == 0 {
				inputType = ""
			}
			urlTemp := b.PathTmpl.Template
			for _, r := range b.PathTmpl.Fields {
				if allFieldsUsedForUrl {
					urlTemp = strings.Replace(urlTemp, fmt.Sprintf("{%s}", r), fmt.Sprintf("${%s}", ToJsonName(r)), -1)
				} else {
					urlTemp = strings.Replace(urlTemp, fmt.Sprintf("{%s}", r), fmt.Sprintf("${%s.%s}", inputName, ToJsonName(r)), -1)
				}
			}
			url := fmt.Sprintf("`${this.host}%s`", urlTemp)
			tempMethod.Execute(&buf, map[string]interface{}{
				"funcName": ToJsonName(m.GetName()),
				"input":    inputType,
				"outType":  g.getRawTypeName(file, m.GetOutputType()),
				"method":   method,
				"body":     body,
				"query":    query,
				"url":      url,
			})
		}
		fmt.Fprintln(&buf, "}")
	}
	return buf.String(), nil
}
func (g *generator) Generate(targets []*descriptor.File) ([]*plugin.CodeGeneratorResponse_File, error) {
	var files []*plugin.CodeGeneratorResponse_File
	for _, file := range targets {
		str, err := g.generate(file)
		if err != nil {
			return nil, err
		}
		name := file.GetName()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		output := fmt.Sprintf("%s.pb.ts", base)
		files = append(files, &plugin.CodeGeneratorResponse_File{
			Name:    proto.String(output),
			Content: proto.String(str),
		})
		glog.V(1).Infof("Will emit %s", output)
	}

	return files, nil
}

func protoComments(reg *descriptor.Registry, file *descriptor.File, outers []string, typeName string, typeIndex int32, fieldPaths ...int32) string {
	if file.SourceCodeInfo == nil {
		// Curious! A file without any source code info.
		// This could be a test that's providing incomplete
		// descriptor.File information.
		//
		// We could simply return no comments, but panic
		// could make debugging easier.
		panic("descriptor.File should not contain nil SourceCodeInfo")
	}

	outerPaths := make([]int32, len(outers))
	for i := range outers {
		location := ""
		if file.Package != nil {
			location = file.GetPackage()
		}

		msg, err := reg.LookupMsg(location, strings.Join(outers[:i+1], "."))
		if err != nil {
			panic(err)
		}
		outerPaths[i] = int32(msg.Index)
	}

	for _, loc := range file.SourceCodeInfo.Location {
		if !isProtoPathMatches(loc.Path, outerPaths, typeName, typeIndex, fieldPaths) {
			continue
		}
		comments := ""
		if loc.LeadingComments != nil {
			comments = strings.TrimRight(*loc.LeadingComments, "\n")
			comments = strings.TrimSpace(comments)
			// TODO(ivucica): this is a hack to fix "// " being interpreted as "//".
			// perhaps we should:
			// - split by \n
			// - determine if every (but first and last) line begins with " "
			// - trim every line only if that is the case
			// - join by \n
			comments = strings.Replace(comments, "\n ", "\n", -1)
		}
		return comments
	}
	return ""
}

var messageProtoPath = protoPathIndex(reflect.TypeOf((*desc.FileDescriptorProto)(nil)), "MessageType")
var nestedProtoPath = protoPathIndex(reflect.TypeOf((*desc.DescriptorProto)(nil)), "NestedType")
var packageProtoPath = protoPathIndex(reflect.TypeOf((*desc.FileDescriptorProto)(nil)), "Package")

func isProtoPathMatches(paths []int32, outerPaths []int32, typeName string, typeIndex int32, fieldPaths []int32) bool {
	if typeName == "Package" && typeIndex == packageProtoPath {
		// path for package comments is just [2], and all the other processing
		// is too complex for it.
		if len(paths) == 0 || typeIndex != paths[0] {
			return false
		}
		return true
	}

	if len(paths) != len(outerPaths)*2+2+len(fieldPaths) {
		return false
	}

	typeNameDescriptor := reflect.TypeOf((*desc.FileDescriptorProto)(nil))
	if len(outerPaths) > 0 {
		if paths[0] != messageProtoPath || paths[1] != outerPaths[0] {
			return false
		}
		paths = paths[2:]
		outerPaths = outerPaths[1:]

		for i, v := range outerPaths {
			if paths[i*2] != nestedProtoPath || paths[i*2+1] != v {
				return false
			}
		}
		paths = paths[len(outerPaths)*2:]

		if typeName == "MessageType" {
			typeName = "NestedType"
		}
		typeNameDescriptor = reflect.TypeOf((*desc.DescriptorProto)(nil))
	}

	if paths[0] != protoPathIndex(typeNameDescriptor, typeName) || paths[1] != typeIndex {
		return false
	}
	paths = paths[2:]

	for i, v := range fieldPaths {
		if paths[i] != v {
			return false
		}
	}
	return true
}

// protoPathIndex returns a path component for google.protobuf.descriptor.SourceCode_Location.
//
// Specifically, it returns an id as generated from descriptor proto which
// can be used to determine what type the id following it in the path is.
// For example, if we are trying to locate comments related to a field named
// `Address` in a message named `Person`, the path will be:
//
//     [4, a, 2, b]
//
// While `a` gets determined by the order in which the messages appear in
// the proto file, and `b` is the field index specified in the proto
// file itself, the path actually needs to specify that `a` refers to a
// message and not, say, a service; and  that `b` refers to a field and not
// an option.
//
// protoPathIndex figures out the values 4 and 2 in the above example. Because
// messages are top level objects, the value of 4 comes from field id for
// `MessageType` inside `google.protobuf.descriptor.FileDescriptor` message.
// This field has a message type `google.protobuf.descriptor.DescriptorProto`.
// And inside message `DescriptorProto`, there is a field named `Field` with id
// 2.
//
// Some code generators seem to be hardcoding these values; this method instead
// interprets them from `descriptor.proto`-derived Go source as necessary.
func protoPathIndex(descriptorType reflect.Type, what string) int32 {
	field, ok := descriptorType.Elem().FieldByName(what)
	if !ok {
		panic(fmt.Errorf("Could not find protobuf descriptor type id for %s.", what))
	}
	pbtag := field.Tag.Get("protobuf")
	if pbtag == "" {
		panic(fmt.Errorf("No Go tag 'protobuf' on protobuf descriptor for %s.", what))
	}
	path, err := strconv.Atoi(strings.Split(pbtag, ",")[1])
	if err != nil {
		panic(fmt.Errorf("Protobuf descriptor id for %s cannot be converted to a number: %s", what, err.Error()))
	}

	return int32(path)
}
