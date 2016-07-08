package genangular

import (
	"fmt"
	"strings"
	"bytes"
	"github.com/gengo/grpc-gateway/protoc-gen-grpc-gateway/descriptor"
	gen "github.com/gengo/grpc-gateway/protoc-gen-grpc-gateway/generator"
	"github.com/golang/protobuf/proto"
	desc "github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"io"
)

type generator struct {
	reg       *descriptor.Registry
	mapValues []string
}

// New returns a new generator which generates grpc gateway files.
func New(reg *descriptor.Registry) gen.Generator {
	return &generator{reg: reg, mapValues: []string{}}
}

func (g *generator) getRawTypeName(a string) string {
	dds := strings.Split(a, ".")
	return dds[len(dds) - 1]
}

func (g *generator) getTypeName(t desc.FieldDescriptorProto_Type, field *desc.FieldDescriptorProto) string {
	switch t {
	case desc.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case desc.FieldDescriptorProto_TYPE_BOOL:
		return "boolean"
	case desc.FieldDescriptorProto_TYPE_FIXED32, desc.FieldDescriptorProto_TYPE_FIXED64, desc.FieldDescriptorProto_TYPE_DOUBLE:
		return "number"
	case desc.FieldDescriptorProto_TYPE_FLOAT, desc.FieldDescriptorProto_TYPE_INT32, desc.FieldDescriptorProto_TYPE_INT64:
		return "number"
	case desc.FieldDescriptorProto_TYPE_UINT32, desc.FieldDescriptorProto_TYPE_UINT64:
		return "number"
	case desc.FieldDescriptorProto_TYPE_ENUM:
		return g.getRawTypeName(field.GetTypeName())
	case desc.FieldDescriptorProto_TYPE_MESSAGE:
		return g.getRawTypeName(field.GetTypeName())
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

func (g *generator) printMessageField(w io.Writer, field *desc.FieldDescriptorProto) {
	if g.isMap(field) {
		fmt.Fprintf(w, "    %s: any;\n", field.GetJsonName())
	} else if field.GetLabel() == desc.FieldDescriptorProto_LABEL_REPEATED {
		fmt.Fprintf(w, "    %s: %s[];\n", field.GetJsonName(), g.getTypeName(field.GetType(), field))
	} else {
		fmt.Fprintf(w, "    %s: %s;\n", field.GetJsonName(), g.getTypeName(field.GetType(), field))
	}
}

func ToJsonName(pre string) string {
	if len(pre) == 0 {
		return ""
	}
	word := pre[:1]
	ss := make([]string, 0)
	for i := 1; i < len(pre); i++ {
		letter := pre[i:i + 1]
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

func (g *generator) Generate(targets []*descriptor.File) ([]*plugin.CodeGeneratorResponse_File, error) {
	var files []*plugin.CodeGeneratorResponse_File
	var messages bytes.Buffer
	var service bytes.Buffer

	fmt.Fprintln(&service, `
// Code generated by protoc-gen-angular.
// DO NOT EDIT!
import {Injectable} from '@angular/core';
import {Http, Response, RequestOptions, RequestMethod, Headers} from "@angular/http";
import {Observable} from "rxjs/Observable";
import * as models from "./models";
import {AuthService} from "./auth.service"
`)
	fmt.Fprintln(&messages, `
// Code generated by protoc-gen-angular.
// DO NOT EDIT!
`)
	for _, file := range targets {
		pack := file.GetPackage()
		for _, m := range file.Messages {
			mesName := fmt.Sprintf(".%s.%s", pack, m.GetName())
			for _, n := range m.GetNestedType() {
				if n.GetOptions().GetMapEntry() {
					g.mapValues = append(g.mapValues, fmt.Sprintf("%s.%s", mesName, n.GetName()))
				}
			}
		}
	}
	for _, file := range targets {
		for _, e := range file.Enums {
			fmt.Fprintf(&messages, "export declare enum %s {\n", e.GetName())
			for _, v := range e.GetValue() {
				fmt.Fprintf(&messages, "    %s = %d,\n", v.GetName(), v.GetNumber())
			}
			fmt.Fprintln(&messages, "}")
		}

		for _, m := range file.Messages {
			//	glog.Errorf("message %v", m)
			if m.GetOptions().GetMapEntry() {
				continue
			}
			fmt.Fprintf(&messages, "export class %s {\n", m.GetName())
			for _, f := range m.GetField() {
				g.printMessageField(&messages, f)
				//	glog.Errorf("  field %s name '%s':'%v':'%s'", f.GetJsonName(), f.GetName(), f.GetType(), f.GetTypeName())
			}
			fmt.Fprintln(&messages, "}")
		}

		for _, s := range file.Services {
			fmt.Fprintf(&service, `
@Injectable()
export class %sService {
  host: string;

  constructor(private http: Http, private auth:AuthService) {
    this.host = "http://localhost:18866";
  }

  private _extractData(res: Response) {
    let body = res.json();
    return body || {};
  }

  private _handleError(error: any, cauth: Observable<void>) {
    let errMsg = (error.message) ? error.message :
      (error.status ? error.status+" - "+error.statusText : 'Server error')
		console.error(errMsg);
		return Observable.throw(errMsg);
  }
`, s.GetName())
			for _, m := range s.Methods {
				if len(m.Bindings) == 0 {
					continue
				}
				b := m.Bindings[0]
				method := b.HTTPMethod[:1] + strings.ToLower(b.HTTPMethod[1:])
				pack := *file.Package
				allFieldsUsedForUrl := false
				var inMessage *descriptor.Message
				for _, mes := range file.Messages {
					mName := fmt.Sprintf(".%s.%s", pack, mes.GetName())
					if mName == m.GetInputType() {
						allFieldsUsedForUrl = len(mes.Fields) == len(b.PathTmpl.Fields)
						inMessage = mes
						break
					}
				}
				inputName := ToJsonName(g.getRawTypeName(m.GetInputType()))
				body := fmt.Sprintf("body: JSON.stringify(%s),", inputName)
				if method == "Get" || method == "Delete" {
					body = ""
				}
				if allFieldsUsedForUrl && (method == "Post" || method == "Put") {
					body = "body: \"{}\","
				}
				inputType := fmt.Sprintf("%s: models.%s", inputName, g.getRawTypeName(m.GetInputType()))
				if allFieldsUsedForUrl {
					inputType = ""
					for _, f := range inMessage.GetField() {
						inputType = fmt.Sprintf("%s,%s:%s", inputType, f.GetJsonName(), g.getTypeName(f.GetType(), f))
					}
					inputType = inputType[1:]
				}
				temp := b.PathTmpl.Template
				for _, r := range b.PathTmpl.Fields {
					if allFieldsUsedForUrl {
						temp = strings.Replace(temp, fmt.Sprintf("{%s}", r), fmt.Sprintf("${%s}", ToJsonName(r)), -1)
					} else {
						temp = strings.Replace(temp, fmt.Sprintf("{%s}", r), fmt.Sprintf("${%s.%s}", inputName, ToJsonName(r)), -1)
					}
				}
				url := fmt.Sprintf("`${this.host}%s`", temp)

				fmt.Fprintf(&service, `
  %s(%s): Observable<models.%s> {
    let _headers = new Headers();
    this.auth.setAuthToHeaders(_headers);
    let _args = new RequestOptions({
      method: RequestMethod.%s,
      headers: _headers,
      %s
    });
    return this.http.request(%s, _args)
      .map(this._extractData)
      .catch(this._handleError);
  }
`, ToJsonName(m.GetName()), inputType, g.getRawTypeName(m.GetOutputType()), method, body, url)
			}
			fmt.Fprintln(&service, "}")
		}
	}
	files = append(files, &plugin.CodeGeneratorResponse_File{
		Name:    proto.String("models.ts"),
		Content: proto.String(messages.String()),
	})
	files = append(files, &plugin.CodeGeneratorResponse_File{
		Name:    proto.String("asset.service.ts"),
		Content: proto.String(service.String()),
	})
	return files, nil
}
