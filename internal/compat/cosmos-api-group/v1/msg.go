package v1

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/runtime/protoimpl"
)

type MsgSubmitProposal struct {
	Proposers []string `protobuf:"bytes,2,rep,name=proposers,proto3" json:"proposers,omitempty"`
	state     protoimpl.MessageState
}

func (m *MsgSubmitProposal) Reset() {
	*m = MsgSubmitProposal{}
}

func (m *MsgSubmitProposal) String() string {
	return protoimpl.X.MessageStringOf(m)
}

func (*MsgSubmitProposal) ProtoMessage() {}

func (m *MsgSubmitProposal) ProtoReflect() protoreflect.Message {
	mi := &file_cosmos_group_v1_msg_msgTypes[0]
	if protoimpl.UnsafeEnabled && m != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(m))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(m)
}

var file_cosmos_group_v1_msg_msgTypes = make([]protoimpl.MessageInfo, 1)
