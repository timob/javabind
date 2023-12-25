// Copyright 2016 Tim O'Brien. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package javabind

import (
	"errors"
	"log"
	"reflect"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"tekao.net/jnigi"
)

const (
	Void    = jnigi.Void
	Boolean = jnigi.Boolean
	Byte    = jnigi.Byte
	Char    = jnigi.Char
	Short   = jnigi.Short
	Int     = jnigi.Int
	Long    = jnigi.Long
	Float   = jnigi.Float
	Double  = jnigi.Double
	Object  = jnigi.Object
	Array   = jnigi.Array
)

var NewObjectRef = jnigi.NewObjectRef

var NewObjectArrayRef = jnigi.NewObjectArrayRef

func ObjectType(className string) jnigi.ObjectType {
	return jnigi.ObjectType(className)
}

func ObjectArrayType(className string) jnigi.ObjectArrayType {
	return jnigi.ObjectArrayType(className)
}

func WrapJObject(jobj uintptr, className string, isArray bool) *jnigi.ObjectRef {
	return jnigi.WrapJObject(jobj, className, isArray)
}

func MakeGlobal(o CallableContainer) {
	g := GetEnv().NewGlobalRef(o.GetCallable().ObjectRef)
	o.GetCallable().ObjectRef = g
}

func DeleteLocalRef(o CallableContainer) {
	GetEnv().DeleteLocalRef(o.GetCallable().ObjectRef)
}

func DeleteGlobalRef(o CallableContainer) {
	GetEnv().DeleteGlobalRef(o.GetCallable().ObjectRef)
}

var debug = false

var jvm *jnigi.JVM

var envs = make(map[int]*jnigi.Env)
var envsLock sync.Mutex

func GetEnv() (e *jnigi.Env) {
	envsLock.Lock()
	if v, ok := envs[GetThreadId()]; !ok {
		// possibly do automatic attachment of thread here
		panic("java method being called from a non attached thread")
	} else {
		e = v
	}
	envsLock.Unlock()
	return
}

type CheckEnv struct {
	e *jnigi.Env
}

func NewCheckEnv() *CheckEnv {
	return &CheckEnv{GetEnv()}
}

func (c *CheckEnv) SameEnv() bool {
	if c != nil {
		if v, ok := envs[GetThreadId()]; ok {
			if v == c.e {
				return true
			}
		}
	}
	return false
}

var OnJVMStartFn []func()

func OnJVMStart(f func()) {
	OnJVMStartFn = append(OnJVMStartFn, f)
}

type AttachedThread struct {
	work         chan func()
	workFinished chan byte
	quit         chan byte
}

func NewAttachedThread() *AttachedThread {
	a := &AttachedThread{make(chan func()), make(chan byte), make(chan byte)}
	go func() {
		runtime.LockOSThread()
		env := jvm.AttachCurrentThread()
		envsLock.Lock()
		envs[GetThreadId()] = env
		envsLock.Unlock()
		for {
			select {
			case f := <-a.work:
				f()
				a.workFinished <- 1
			case <-a.quit:
				envsLock.Lock()
				delete(envs, GetThreadId())
				envsLock.Unlock()
				if err := jvm.DetachCurrentThread(env); err != nil {
					log.Print(err)
				}
				return
			}
		}
	}()
	return a
}

func (a *AttachedThread) Run(f func()) {
	a.work <- f
	<-a.workFinished
}

func (a *AttachedThread) Stop() {
	a.quit <- 1
}

func SetupJVM(classPath string) (err error) {
	//classPath = append(classPath, gojvm.DefaultJREPath)
	if debug {
		log.Printf("Using classpath %v", classPath)
	}
	libPath := jnigi.AttemptToFindJVMLibPath()
	if err := jnigi.LoadJVMLib(libPath); err != nil {
		log.Printf("library path = %s", libPath)
		log.Printf("can use JAVA_HOME environment variable to set JRE root directory")
		return err
	}

	runtime.LockOSThread()
	args := []string{"-Djava.class.path=" + classPath}
	// []string{"-Xcheck:jni"}
	jvm2, env, err := jnigi.CreateJVM(jnigi.NewJVMInitArgs(false, true, jnigi.DEFAULT_VERSION, args))
	if err != nil {
		return err
	}
	jvm = jvm2
	if jvm == nil {
		return errors.New("Got a nil context!")
	}
	envs[GetThreadId()] = env

	for _, f := range OnJVMStartFn {
		f()
	}

	return
}

func StopJvm() error {
	return jvm.Destroy()
}

func SetupJVMFromEnv(env unsafe.Pointer) {
	envs[GetThreadId()] = jnigi.WrapEnv(env)
	for _, f := range OnJVMStartFn {
		f()
	}
}

func AddEnv(env unsafe.Pointer) {
	envs[GetThreadId()] = jnigi.WrapEnv(env)
}

func CallObjectMethod(obj *jnigi.ObjectRef, env *jnigi.Env, methodName string, dest interface{}, args ...interface{}) error {
	err := obj.CallMethod(env, methodName, dest, args...)
	if err != nil {
		return err
	}
	return nil
}

type Callable struct {
	*jnigi.ObjectRef
	nonVirtual bool
}

func NewCallable(of *jnigi.ObjectRef) *Callable {
	return &Callable{ObjectRef: of}
}

func NewEmptyCallable() *Callable {
	return &Callable{}
}

func (callable *Callable) SetNonVirtual() {
	callable.nonVirtual = true
}

func (callable *Callable) CallMethod(env *jnigi.Env, methodName string, dest interface{}, args ...interface{}) (err error) {
	if callable.nonVirtual {
		err = callable.ObjectRef.CallNonvirtualMethod(env, callable.ObjectRef.GetClassName(), methodName, dest, args...)
	} else {
		err = callable.ObjectRef.CallMethod(env, methodName, dest, args...)
	}
	return err
}

type CallableContainer interface {
	GetCallable() *Callable
}

func (c *Callable) GetCallable() *Callable {
	return c
}

func (c *Callable) InstanceOf(className string) bool {
	r, err := c.ObjectRef.IsInstanceOf(GetEnv(), className)
	if err != nil {
		return false
	}
	return r
}

func size(env *jnigi.Env, obj *jnigi.ObjectRef) (int, error) {
	var v int
	err := obj.CallMethod(env, "size", &v)
	if err != nil {
		return 0, err
	}
	return v, nil
}

type ToJavaConverter interface {
	Convert(value interface{}) error
	Value() *jnigi.ObjectRef
	CleanUp() error
}

func ObjectRef(v interface{}) *jnigi.ObjectRef {
	return v.(*jnigi.ObjectRef)
}

type FromJavaConverter interface {
	Dest(ptr interface{})
	Convert(obj *jnigi.ObjectRef) error
	CleanUp() error
}

type GoToJavaCallable struct {
	obj *jnigi.ObjectRef
}

func NewGoToJavaCallable() *GoToJavaCallable {
	return &GoToJavaCallable{}
}

func (g *GoToJavaCallable) Convert(value interface{}) (err error) {
	g.obj = value.(CallableContainer).GetCallable().ObjectRef
	return
}

func (g *GoToJavaCallable) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaCallable) CleanUp() error {
	return nil
}

type JavaToGoCallable struct {
	callable *Callable
}

func NewJavaToGoCallable() *JavaToGoCallable {
	return &JavaToGoCallable{}
}

func (j *JavaToGoCallable) Dest(ptr interface{}) {
	j.callable = ptr.(CallableContainer).GetCallable()
}

func (j *JavaToGoCallable) Convert(obj *jnigi.ObjectRef) (err error) {
	j.callable.ObjectRef = obj
	return
}

func (j *JavaToGoCallable) CleanUp() error {
	return nil
}

type GoToJavaString struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaString() *GoToJavaString {
	return &GoToJavaString{env: GetEnv()}
}

func (g *GoToJavaString) Convert(value interface{}) (err error) {
	g.env.PrecalculateSignature("([BLjava/lang/String;)V")
	g.obj, err = g.env.NewObject("java/lang/String", []byte(value.(string)), g.env.GetUTF8String())
	return
}

func (g *GoToJavaString) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaString) CleanUp() error {
	if !g.obj.IsNil() {
		g.env.DeleteLocalRef(g.obj)
	}
	return nil
}

type JavaToGoString struct {
	str *string
	env *jnigi.Env
	obj *jnigi.ObjectRef
}

func NewJavaToGoString() *JavaToGoString {
	return &JavaToGoString{env: GetEnv()}
}

func (j *JavaToGoString) Dest(ptr interface{}) {
	j.str = ptr.(*string)
}

func (j *JavaToGoString) Convert(obj *jnigi.ObjectRef) error {
	// Java empty string is represented by nil
	if obj.IsNil() {
		*j.str = ""
		return nil
	}

	j.obj = obj

	j.env.PrecalculateSignature("(Ljava/lang/String;)[B")
	var strBytes []byte
	err := j.obj.CallMethod(j.env, "getBytes", &strBytes, j.env.GetUTF8String())
	if err != nil {
		return err
	}
	*j.str = string(strBytes)

	return nil
}

func (j *JavaToGoString) CleanUp() (err error) {
	if j.obj != nil {
		j.env.DeleteLocalRef(j.obj)
	}
	return
}

type GoToJavaList struct {
	obj  *jnigi.ObjectRef
	env  *jnigi.Env
	item ToJavaConverter
}

func NewGoToJavaList(item ToJavaConverter) *GoToJavaList {
	return &GoToJavaList{env: GetEnv(), item: item}
}

func (g *GoToJavaList) Convert(value interface{}) (err error) {
	r_value := reflect.ValueOf(value)
	if r_value.Type().Kind() != reflect.Slice {
		return errors.New("GoToJavaList.Convert: value not slice")
	}
	n := r_value.Len()

	listObj, err := g.env.NewObject("java/util/ArrayList", n)
	if err != nil {
		return
	}

	for i := 0; i < n; i++ {
		if err = g.item.Convert(r_value.Index(i).Interface()); err != nil {
			return
		}

		//		g.env.PrecalculateSignature("(Ljava/lang/Object;)Z")
		var dummy bool
		err = listObj.CallMethod(g.env, "add", &dummy, g.item.Value().Cast("java/lang/Object"))
		if err != nil {
			return
		}

		if err = g.item.CleanUp(); err != nil {
			return
		}
	}
	g.obj = listObj.Cast("java/util/List")

	return
}

func (g *GoToJavaList) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaList) CleanUp() error {
	g.env.DeleteLocalRef(g.obj)
	return nil
}

type JavaToGoList struct {
	list interface{}
	env  *jnigi.Env
	item FromJavaConverter
}

func NewJavaToGoList(item FromJavaConverter) *JavaToGoList {
	return &JavaToGoList{env: GetEnv(), item: item}
}

func (j *JavaToGoList) Dest(ptr interface{}) {
	j.list = ptr
}

func (j *JavaToGoList) Convert(obj *jnigi.ObjectRef) (err error) {
	r_value := reflect.ValueOf(j.list)

	if r_value.Type().Kind() != reflect.Ptr {
		return errors.New("JavaToGoList.Convert: dest not ptr")
	}

	r_slice := reflect.Indirect(r_value)
	if r_slice.Type().Kind() != reflect.Slice {
		return errors.New("JavaToGoList.Convert: dest ptr , does not point to slice")
	}

	len, err := size(j.env, obj)
	if err != nil {
		return
	}
	for i := 0; i < len; i++ {
		itemObj := jnigi.NewObjectRef("java/lang/Object")
		err := CallObjectMethod(obj, j.env, "get", itemObj, i)
		if err != nil {
			return err
		}

		//TODO change this: assumes that if slice element is a pointer it is a generated callable
		r_newElem := reflect.Indirect(reflect.New(r_slice.Type().Elem()))
		if r_newElem.Type().Kind() == reflect.Ptr {
			r_elemVal := reflect.New(r_newElem.Type().Elem())
			r_newElem.Set(r_elemVal)
			c := NewEmptyCallable()
			// this is the pointer to generated type by JAG
			reflect.Indirect(r_elemVal).FieldByName("Callable").Set(reflect.ValueOf(c))
			j.item.Dest(c)
		} else {
			j.item.Dest(r_newElem.Addr().Interface())
		}
		if err = j.item.Convert(itemObj); err != nil {
			return err
		}
		if err = j.item.CleanUp(); err != nil {
			return err
		}

		r_newSlice := reflect.Append(r_slice, r_newElem)
		r_slice.Set(r_newSlice)
	}

	return
}

func (j *JavaToGoList) CleanUp() (err error) {
	return
}

type JavaToGoIterator struct {
	list interface{}
	env  *jnigi.Env
	item FromJavaConverter
}

func NewJavaToGoIterator(item FromJavaConverter) *JavaToGoIterator {
	return &JavaToGoIterator{env: GetEnv(), item: item}
}

func (j *JavaToGoIterator) Dest(ptr interface{}) {
	j.list = ptr
}

func (j *JavaToGoIterator) Convert(obj *jnigi.ObjectRef) (err error) {
	r_value := reflect.ValueOf(j.list)

	if r_value.Type().Kind() != reflect.Ptr {
		return errors.New("JavaToGoList.Convert: dest not ptr")
	}

	r_slice := reflect.Indirect(r_value)
	if r_slice.Type().Kind() != reflect.Slice {
		return errors.New("JavaToGoList.Convert: dest ptr , does not point to slice")
	}

	for {
		var v bool
		err := obj.CallMethod(j.env, "hasNext", &v)
		if err != nil {
			return err
		}
		if v == false {
			break
		}

		next := jnigi.NewObjectRef("java/lang/Object")
		err = CallObjectMethod(obj, j.env, "next", next)
		if err != nil {
			return err
		}

		r_newElem := reflect.Indirect(reflect.New(r_slice.Type().Elem()))
		j.item.Dest(r_newElem.Addr().Interface())
		if err = j.item.Convert(next); err != nil {
			return err
		}
		if err = j.item.CleanUp(); err != nil {
			return err
		}

		r_newSlice := reflect.Append(r_slice, r_newElem)
		r_slice.Set(r_newSlice)
	}

	return
}

func (j *JavaToGoIterator) CleanUp() (err error) {
	return
}

type GoToJavaCollection struct {
	*GoToJavaList
}

func NewGoToJavaCollection(item ToJavaConverter) *GoToJavaCollection {
	return &GoToJavaCollection{NewGoToJavaList(item)}
}

func (g *GoToJavaCollection) Value() *jnigi.ObjectRef {
	return g.GoToJavaList.Value().Cast("java/util/Collection")
}

type JavaToGoCollection struct {
	env  *jnigi.Env
	iter *jnigi.ObjectRef
	*JavaToGoIterator
}

func NewJavaToGoCollection(item FromJavaConverter) *JavaToGoCollection {
	return &JavaToGoCollection{GetEnv(), nil, NewJavaToGoIterator(item)}
}

func (j *JavaToGoCollection) Dest(ptr interface{}) {
	j.JavaToGoIterator.Dest(ptr)
}

func (j *JavaToGoCollection) Convert(obj *jnigi.ObjectRef) (err error) {
	iter := jnigi.NewObjectRef("java/util/Iterator")
	err = CallObjectMethod(obj, j.env, "iterator", iter)
	if err != nil {
		return
	}
	j.iter = iter
	return j.JavaToGoIterator.Convert(j.iter)
}

func (j *JavaToGoCollection) CleanUp() (err error) {
	j.env.DeleteLocalRef(j.iter)
	return
}

type GoToJavaSet struct {
	obj  *jnigi.ObjectRef
	env  *jnigi.Env
	item ToJavaConverter
}

// needs to be hashset
func NewGoToJavaSet(item ToJavaConverter) *GoToJavaSet {
	return &GoToJavaSet{env: GetEnv(), item: item}
}

func (g *GoToJavaSet) Convert(value interface{}) (err error) {
	r_value := reflect.ValueOf(value)
	if r_value.Type().Kind() != reflect.Slice {
		return errors.New("GoToJavaSet.Convert: value not slice")
	}
	n := r_value.Len()

	hashObj, err := g.env.NewObject("java/util/HashSet", n)
	if err != nil {
		return
	}

	for i := 0; i < n; i++ {
		if err = g.item.Convert(r_value.Index(i).Interface()); err != nil {
			return
		}

		//		g.env.PrecalculateSignature("(Ljava/lang/Object;)Z")
		var dummy bool
		err = hashObj.CallMethod(g.env, "add", &dummy, g.item.Value().Cast("java/lang/Object"))
		if err != nil {
			return
		}

		if err = g.item.CleanUp(); err != nil {
			return
		}
	}
	g.obj = hashObj.Cast("java/util/Set")

	return
}

func (g *GoToJavaSet) Value() *jnigi.ObjectRef {
	return g.obj
}

type JavaToGoSet struct {
	env  *jnigi.Env
	iter *jnigi.ObjectRef
	*JavaToGoIterator
}

func NewJavaToGoSet(item FromJavaConverter) *JavaToGoSet {
	return &JavaToGoSet{GetEnv(), nil, NewJavaToGoIterator(item)}
}

func (j *JavaToGoSet) Dest(ptr interface{}) {
	j.JavaToGoIterator.Dest(ptr)
}

func (j *JavaToGoSet) Convert(obj *jnigi.ObjectRef) (err error) {
	iter := jnigi.NewObjectRef("java/util/Iterator")
	err = CallObjectMethod(obj, j.env, "iterator", iter)
	if err != nil {
		return
	}
	j.iter = iter
	return j.JavaToGoIterator.Convert(j.iter)
}

func (j *JavaToGoSet) CleanUp() (err error) {
	j.env.DeleteLocalRef(j.iter)
	return
}

type GoToJavaMap struct {
	obj   *jnigi.ObjectRef
	env   *jnigi.Env
	key   ToJavaConverter
	value ToJavaConverter
}

func NewGoToJavaMap(key, value ToJavaConverter) *GoToJavaMap {
	return &GoToJavaMap{env: GetEnv(), key: key, value: value}
}

func (g *GoToJavaMap) Convert(value interface{}) (err error) {
	mapObj, err := g.env.NewObject("java/util/HashMap")
	if err != nil {
		return
	}

	r_value := reflect.ValueOf(value)
	if r_value.Type().Kind() != reflect.Map {
		return errors.New("GoToJavaMap.Convert: value not map")
	}

	for _, r_kv := range r_value.MapKeys() {
		err = g.key.Convert(r_kv.Interface())
		if err != nil {
			return
		}
		err = g.value.Convert(r_value.MapIndex(r_kv).Interface())
		if err != nil {
			return
		}

		dummy := jnigi.NewObjectRef("java/lang/Object")
		err = mapObj.CallMethod(g.env, "put", dummy, g.key.Value().Cast("java/lang/Object"), g.value.Value().Cast("java/lang/Object"))
		if err != nil {
			return
		}
		if err = g.key.CleanUp(); err != nil {
			return
		}
		if err = g.value.CleanUp(); err != nil {
			return
		}
	}
	g.obj = mapObj.Cast("java/util/Map")

	return
}

func (g *GoToJavaMap) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaMap) CleanUp() error {
	g.env.DeleteLocalRef(g.obj)
	return nil
}

type JavaToGoMap struct {
	mapval interface{}
	env    *jnigi.Env
	key    FromJavaConverter
	value  FromJavaConverter
	list   *jnigi.ObjectRef
}

func NewJavaToGoMap(key, value FromJavaConverter) *JavaToGoMap {
	return &JavaToGoMap{env: GetEnv(), key: key, value: value}
}

func (j *JavaToGoMap) Dest(ptr interface{}) {
	j.mapval = ptr
}

func (j *JavaToGoMap) Convert(obj *jnigi.ObjectRef) (err error) {
	r_value := reflect.ValueOf(j.mapval)

	if r_value.Type().Kind() != reflect.Ptr {
		return errors.New("JavaToGoMap.Convert: dest not ptr")
	}

	r_map := reflect.Indirect(r_value)
	if r_map.Type().Kind() != reflect.Map {
		return errors.New("JavaToGoMap.Convert: dest ptr , does not point to map")
	}

	if r_map.IsNil() {
		r_map.Set(reflect.MakeMap(r_map.Type()))
	}

	keySet := jnigi.NewObjectRef("java/util/Set")
	err = CallObjectMethod(obj, j.env, "keySet", keySet)
	if err != nil {
		return
	}
	keyList, err := j.env.NewObject("java/util/ArrayList", keySet.Cast("java/util/Collection"))
	if err != nil {
		return
	}
	j.env.DeleteLocalRef(keySet)
	j.list = keyList

	len, err := size(j.env, obj)
	if err != nil {
		return
	}
	for i := 0; i < len; i++ {
		keyobj := jnigi.NewObjectRef("java/lang/Object")
		err := CallObjectMethod(keyList, j.env, "get", keyobj, i)
		if err != nil {
			return err
		}
		valobj := jnigi.NewObjectRef("java/lang/Object")
		err = CallObjectMethod(obj, j.env, "get", valobj, keyobj.Cast("java/lang/Object"))
		if err != nil {
			return err
		}

		r_newKey := reflect.Indirect(reflect.New(r_map.Type().Key()))
		j.key.Dest(r_newKey.Addr().Interface())
		r_newVal := reflect.Indirect(reflect.New(r_map.Type().Elem()))

		//TODO: fix that this assumes if pointer then callable
		if r_newVal.Type().Kind() == reflect.Ptr {
			r_elemVal := reflect.New(r_newVal.Type().Elem())
			r_newVal.Set(r_elemVal)
			c := NewEmptyCallable()
			reflect.Indirect(r_elemVal).FieldByName("Callable").Set(reflect.ValueOf(c))
			j.value.Dest(c)
		} else {
			j.value.Dest(r_newVal.Addr().Interface())
		}

		//		j.value.Dest(r_newVal.Addr().Interface())

		if err = j.key.Convert(keyobj); err != nil {
			return err
		}
		if err = j.value.Convert(valobj); err != nil {
			return err
		}

		if err = j.key.CleanUp(); err != nil {
			return err
		}
		if err = j.value.CleanUp(); err != nil {
			return err
		}

		r_map.SetMapIndex(r_newKey, r_newVal)
	}

	return
}

func (j *JavaToGoMap) CleanUp() (err error) {
	j.env.DeleteLocalRef(j.list)
	return
}

type JavaToGoMap_Entry struct {
	entry interface{}
	env   *jnigi.Env
	key   FromJavaConverter
	value FromJavaConverter
}

func NewJavaToGoMap_Entry(key, value FromJavaConverter) *JavaToGoMap_Entry {
	return &JavaToGoMap_Entry{key: key, value: value, env: GetEnv()}
}

func (j *JavaToGoMap_Entry) Dest(ptr interface{}) {
	j.entry = ptr
}

func (j *JavaToGoMap_Entry) Convert(obj *jnigi.ObjectRef) (err error) {
	r_value := reflect.ValueOf(j.entry)

	if r_value.Type().Kind() != reflect.Ptr {
		return errors.New("JavaToGoMapEntry.Convert: dest not ptr")
	}

	r_struct := reflect.Indirect(r_value)
	if r_struct.Type().Kind() != reflect.Struct {
		return errors.New("JavaToGoMapEntry.Convert: dest ptr , does not point to struct")
	}

	keyObj := jnigi.NewObjectRef("java/lang/Object")
	err = CallObjectMethod(obj, j.env, "getKey", keyObj)
	if err != nil {
		return
	}
	valObj := jnigi.NewObjectRef("java/lang/Object")
	err = CallObjectMethod(obj, j.env, "getValue", valObj)
	if err != nil {
		return
	}

	r_structKey := r_struct.FieldByName("Key")
	j.key.Dest(r_structKey.Addr().Interface())
	callable := NewEmptyCallable()
	j.value.Dest(callable)
	if err = j.key.Convert(keyObj); err != nil {
		return err
	}
	if err = j.value.Convert(valObj); err != nil {
		return err
	}

	r_structVal := r_struct.FieldByName("Value")
	r_structValNew := reflect.New(r_structVal.Type().Elem())
	reflect.Indirect(r_structValNew).FieldByName("Callable").Set(reflect.ValueOf(callable))
	r_structVal.Set(r_structValNew)

	if err = j.key.CleanUp(); err != nil {
		return err
	}
	if err = j.value.CleanUp(); err != nil {
		return err
	}

	return
}

func (j *JavaToGoMap_Entry) CleanUp() (err error) {
	return
}

type GoToJavaObjectArray struct {
	item        ToJavaConverter
	objectArray *jnigi.ObjectRef
	env         *jnigi.Env
	className   string
	list        []*jnigi.ObjectRef
}

func NewGoToJavaObjectArray(item ToJavaConverter, className string) *GoToJavaObjectArray {
	return &GoToJavaObjectArray{item: item, env: GetEnv(), className: className}
}

func (g *GoToJavaObjectArray) Convert(value interface{}) (err error) {

	r_value := reflect.ValueOf(value)
	if r_value.Type().Kind() != reflect.Slice {
		return errors.New("GoToJavaObjectArray.Convert: value not slice")
	}

	n := r_value.Len()
	a := make([]*jnigi.ObjectRef, n)
	for i := 0; i < n; i++ {
		if err = g.item.Convert(r_value.Index(i).Interface()); err != nil {
			return
		}

		a[i] = g.item.Value()
		/*
			if err = g.item.CleanUp(); err != nil {
				return
			}
		*/
	}
	g.objectArray = g.env.ToObjectArray(a, g.className)
	g.list = a
	return
}

func (g *GoToJavaObjectArray) Value() *jnigi.ObjectRef {
	return g.objectArray
}

func (g *GoToJavaObjectArray) CleanUp() error {
	// assuming here the references were created in the conveter, so delete them after the array is made/used
	for _, r := range g.list {
		g.env.DeleteLocalRef(r)
	}

	g.env.DeleteLocalRef(g.objectArray)
	return nil
}

type JavaToGoObjectArray struct {
	item FromJavaConverter
	list interface{}
}

func NewJavaToGoObjectArray(item FromJavaConverter, className string) *JavaToGoObjectArray {
	return &JavaToGoObjectArray{item: item}
}

func (j *JavaToGoObjectArray) Dest(ptr interface{}) {
	j.list = ptr
}

func (j *JavaToGoObjectArray) Convert(obj *jnigi.ObjectRef) (err error) {
	objs := GetEnv().FromObjectArray(obj)

	r_value := reflect.ValueOf(j.list)

	if r_value.Type().Kind() != reflect.Ptr {
		return errors.New("JavaToGoList.Convert: dest not ptr")
	}

	r_slice := reflect.Indirect(r_value)
	if r_slice.Type().Kind() != reflect.Slice {
		return errors.New("JavaToGoList.Convert: dest ptr , does not point to slice")
	}

	etype := r_slice.Type().Elem()

	if etype.Kind() == reflect.Ptr {
		for i := 0; i < len(objs); i++ {
			r_newElemV := reflect.New(etype.Elem())
			c := NewEmptyCallable()
			j.item.Dest(c)
			if err = j.item.Convert(objs[i]); err != nil {
				return err
			}
			if err = j.item.CleanUp(); err != nil {
				return err
			}

			reflect.Indirect(r_newElemV).FieldByName("Callable").Set(reflect.ValueOf(c))
			r_newSlice := reflect.Append(r_slice, r_newElemV)
			r_slice.Set(r_newSlice)
		}
	} else if etype.Kind() == reflect.String {
		for i := 0; i < len(objs); i++ {
			dst := new(string)
			j.item.Dest(dst)
			if err = j.item.Convert(objs[i]); err != nil {
				return err
			}
			if err = j.item.CleanUp(); err != nil {
				return err
			}
			r_newSlice := reflect.Append(r_slice, reflect.ValueOf(dst).Elem())
			r_slice.Set(r_newSlice)
		}
	}

	return
}

func (j *JavaToGoObjectArray) CleanUp() (err error) {
	return
}

type GoToJavaInteger struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaInteger() *GoToJavaInteger {
	return &GoToJavaInteger{env: GetEnv()}
}

func (g *GoToJavaInteger) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Integer", value.(int))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaInteger) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaInteger) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoInteger struct {
	intPtr *int
	env    *jnigi.Env
}

func NewJavaToGoInteger() *JavaToGoInteger {
	return &JavaToGoInteger{env: GetEnv()}
}

func (g *JavaToGoInteger) Dest(ptr interface{}) {
	g.intPtr = ptr.(*int)
}

func (g *JavaToGoInteger) Convert(obj *jnigi.ObjectRef) (err error) {
	var v int
	err = obj.CallMethod(g.env, "IntegerValue", &v)
	if err != nil {
		return
	}
	*g.intPtr = v
	return
}

func (g *JavaToGoInteger) CleanUp() (err error) {
	//g.env.DeleteLocalRef(g.obj)
	return
}

type GoToJavaLong struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaLong() *GoToJavaLong {
	return &GoToJavaLong{env: GetEnv()}
}

func (g *GoToJavaLong) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Long", value.(int64))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaLong) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaLong) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoLong struct {
	int64Ptr *int64
	env      *jnigi.Env
}

func NewJavaToGoLong() *JavaToGoLong {
	return &JavaToGoLong{env: GetEnv()}
}

func (g *JavaToGoLong) Dest(ptr interface{}) {
	g.int64Ptr = ptr.(*int64)
}

func (g *JavaToGoLong) Convert(obj *jnigi.ObjectRef) (err error) {
	var v int64
	err = obj.CallMethod(g.env, "longValue", &v)
	if err != nil {
		return
	}
	*g.int64Ptr = v
	return
}

func (g *JavaToGoLong) CleanUp() (err error) {
	//g.env.DeleteLocalRef(g.obj)
	return
}

type GoToJavaInt struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaInt() *GoToJavaInt {
	return &GoToJavaInt{env: GetEnv()}
}

func (g *GoToJavaInt) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Int", value.(int))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaInt) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaInt) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoInt struct {
	intPtr *int
	env    *jnigi.Env
}

func NewJavaToGoInt() *JavaToGoInt {
	return &JavaToGoInt{env: GetEnv()}
}

func (g *JavaToGoInt) Dest(ptr interface{}) {
	g.intPtr = ptr.(*int)
}

func (g *JavaToGoInt) Convert(obj *jnigi.ObjectRef) (err error) {
	var v int
	err = obj.CallMethod(g.env, "intValue", &v)
	if err != nil {
		return
	}
	*g.intPtr = v
	return
}

func (g *JavaToGoInt) CleanUp() (err error) {
	return
}

type GoToJavaFloat struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaFloat() *GoToJavaFloat {
	return &GoToJavaFloat{env: GetEnv()}
}

func (g *GoToJavaFloat) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Float", value.(float32))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaFloat) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaFloat) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoFloat struct {
	floatPtr *float32
	env      *jnigi.Env
}

func NewJavaToGoFloat() *JavaToGoFloat {
	return &JavaToGoFloat{env: GetEnv()}
}

func (g *JavaToGoFloat) Dest(ptr interface{}) {
	g.floatPtr = ptr.(*float32)
}

func (g *JavaToGoFloat) Convert(obj *jnigi.ObjectRef) (err error) {
	var v float32
	err = obj.CallMethod(g.env, "floatValue", &v)
	if err != nil {
		return
	}
	*g.floatPtr = v
	return
}

func (g *JavaToGoFloat) CleanUp() (err error) {
	return
}

type GoToJavaDouble struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaDouble() *GoToJavaDouble {
	return &GoToJavaDouble{env: GetEnv()}
}

func (g *GoToJavaDouble) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Double", value.(float64))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaDouble) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaDouble) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoDouble struct {
	floatPtr *float64
	env      *jnigi.Env
}

func NewJavaToGoDouble() *JavaToGoDouble {
	return &JavaToGoDouble{env: GetEnv()}
}

func (g *JavaToGoDouble) Dest(ptr interface{}) {
	g.floatPtr = ptr.(*float64)
}

func (g *JavaToGoDouble) Convert(obj *jnigi.ObjectRef) (err error) {
	var v float64
	err = obj.CallMethod(g.env, "doubleValue", &v)
	if err != nil {
		return
	}
	*g.floatPtr = v
	return
}

func (g *JavaToGoDouble) CleanUp() (err error) {
	return
}

type GoToJavaBoolean struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaBoolean() *GoToJavaBoolean {
	return &GoToJavaBoolean{env: GetEnv()}
}

func (g *GoToJavaBoolean) Convert(value interface{}) (err error) {
	g.obj, err = g.env.NewObject("java/lang/Bool", value.(bool))
	if err != nil {
		return
	}
	return
}

func (g *GoToJavaBoolean) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaBoolean) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoBoolean struct {
	boolPtr *bool
	env     *jnigi.Env
}

func NewJavaToGoBoolean() *JavaToGoBoolean {
	return &JavaToGoBoolean{env: GetEnv()}
}

func (g *JavaToGoBoolean) Dest(ptr interface{}) {
	g.boolPtr = ptr.(*bool)
}

func (g *JavaToGoBoolean) Convert(obj *jnigi.ObjectRef) (err error) {
	var v bool
	err = obj.CallMethod(g.env, "booleanValue", &v)
	if err != nil {
		return
	}
	*g.boolPtr = v
	return
}

func (g *JavaToGoBoolean) CleanUp() (err error) {
	return
}

type GoToJavaInetAddress struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaInetAddress() *GoToJavaInetAddress {
	return &GoToJavaInetAddress{env: GetEnv()}
}

func (g *GoToJavaInetAddress) Convert(value interface{}) (err error) {
	t := value.(string)

	sconv := NewGoToJavaString()
	if err := sconv.Convert(t); err != nil {
		return err
	}

	v := jnigi.NewObjectRef("java.lang.InetAddress")
	err = g.env.CallStaticMethod("java.net.InetAddress", "getByName", v, sconv.Value())
	if err != nil {
		return err
	}

	sconv.CleanUp()

	g.obj = v
	return
}

func (g *GoToJavaInetAddress) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaInetAddress) CleanUp() (err error) {
	return
}

type JavaToGoInetAddress struct {
	strPtr *string
	env    *jnigi.Env
}

func NewJavaToGoInetAddress() *JavaToGoInetAddress {
	return &JavaToGoInetAddress{env: GetEnv()}
}

func (g *JavaToGoInetAddress) Dest(ptr interface{}) {
	g.strPtr = ptr.(*string)
}

func (g *JavaToGoInetAddress) Convert(obj *jnigi.ObjectRef) (err error) {
	v := jnigi.NewObjectRef("java/lang/String")
	err = obj.CallMethod(g.env, "getHostAddress", v)
	if err != nil {
		return
	}

	sconv := NewJavaToGoString()
	sconv.Dest(g.strPtr)
	sconv.Convert(v)
	sconv.CleanUp()
	return
}

func (g *JavaToGoInetAddress) CleanUp() (err error) {
	return
}

type GoToJavaDate struct {
	obj *jnigi.ObjectRef
	env *jnigi.Env
}

func NewGoToJavaDate() *GoToJavaDate {
	return &GoToJavaDate{env: GetEnv()}
}

// sub second and timzone info lost
func (g *GoToJavaDate) Convert(value interface{}) (err error) {
	t := value.(time.Time)

	g.obj, err = g.env.NewObject("java/util/Date", t.UnixNano()/1000000)
	if err != nil {
		return err
	}

	return
}

func (g *GoToJavaDate) Value() *jnigi.ObjectRef {
	return g.obj
}

func (g *GoToJavaDate) CleanUp() (err error) {
	g.env.DeleteLocalRef(g.obj)
	return
}

type JavaToGoDate struct {
	timePtr *time.Time
	env     *jnigi.Env
}

func NewJavaToGoDate() *JavaToGoDate {
	return &JavaToGoDate{env: GetEnv()}
}

func (g *JavaToGoDate) Dest(ptr interface{}) {
	g.timePtr = ptr.(*time.Time)
}

func (g *JavaToGoDate) Convert(obj *jnigi.ObjectRef) (err error) {
	var v int64
	err = obj.CallMethod(g.env, "getTime", &v)
	if err != nil {
		return
	}
	ms := v
	*g.timePtr = time.Unix(ms/1000, (ms%1000)*1000000)
	return
}

func (g *JavaToGoDate) CleanUp() (err error) {
	return
}
