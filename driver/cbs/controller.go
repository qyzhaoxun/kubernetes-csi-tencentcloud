package cbs

import (
	"strconv"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	cbs "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cbs/v20170312"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	GB = 1 << (10 * 3)

	// cbs disk type
	DiskTypeAttr = "diskType"

	DiskTypeCloudBasic   = "CLOUD_BASIC"
	DiskTypeCloudPremium = "CLOUD_PREMIUM"
	DiskTypeCloudSsd     = "CLOUD_SSD"

	DiskTypeDefault = DiskTypeCloudBasic

	// cbs disk charge type
	DiskChargeTypeAttr           = "diskChargeType"
	DiskChargeTypePrePaid        = "PREPAID"
	DiskChargeTypePostPaidByHour = "POSTPAID_BY_HOUR"

	DiskChargeTypeDefault = DiskChargeTypePostPaidByHour

	// cbs disk charge prepaid options
	DiskChargePrepaidPeriodAttr = "diskChargeTypePrepaidPeriod"

	DiskChargePrepaidPeriodValidValues = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 24, 36}
	DiskChargePrepaidPeriodDefault     = 1

	DiskChargePrepaidRenewFlagAttr = "diskChargePrepaidRenewFlag"

	DiskChargePrepaidRenewFlagNotifyAndAutoRenew          = "NOTIFY_AND_AUTO_RENEW"
	DiskChargePrepaidRenewFlagNotifyAndManualRenewd       = "NOTIFY_AND_MANUAL_RENEW"
	DiskChargePrepaidRenewFlagDisableNotifyAndManualRenew = "DISABLE_NOTIFY_AND_MANUAL_RENEW"

	DiskChargePrepaidRenewFlagDefault = DiskChargePrepaidRenewFlagNotifyAndManualRenewd

	// cbs disk encrypt
	EncryptAttr   = "encrypt"
	EncryptEnable = "ENCRYPT"

	// cbs status
	StatusUnattached = "UNATTACHED"
	StatusAttached   = "ATTACHED"
)

type cbsController struct {
	cbsClient *cbs.Client
	zone      string
}

func newCbsController(secretId, secretKey, region, zone string) (*cbsController, error) {
	client, err := cbs.NewClient(common.NewCredential(secretId, secretKey), region, profile.NewClientProfile())
	if err != nil {
		return nil, err
	}

	return &cbsController{
		cbsClient: client,
		zone:      zone,
	}, nil
}

func (ctrl *cbsController) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is empty")
	}

	volumeIdempotencyName := req.Name
	volumeCapacity := req.CapacityRange.RequiredBytes

	if len(req.VolumeCapabilities) <= 0 {
		return nil, status.Error(codes.InvalidArgument, "volume has no capabilities")
	}

	for _, c := range req.VolumeCapabilities {
		if c.GetBlock() != nil {
			return nil, status.Error(codes.InvalidArgument, "block volume is not supported")
		}
		if c.AccessMode.Mode != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			return nil, status.Error(codes.InvalidArgument, "block access mode only support singer node writer")
		}
	}

	volumeType, ok := req.Parameters[DiskTypeAttr]
	if !ok {
		volumeType = DiskTypeDefault
	}

	if volumeType != DiskTypeCloudBasic && volumeType != DiskTypeCloudPremium && volumeType != DiskTypeCloudSsd {
		return nil, status.Error(codes.InvalidArgument, "cbs type not supported")
	}

	volumeChargeType, ok := req.Parameters[DiskChargeTypeAttr]
	if !ok {
		volumeChargeType = DiskChargeTypeDefault
	}

	var volumeChargePrepaidPeriod int
	var volumeChargePrepaidRenewFlag string

	if volumeChargeType == DiskChargeTypePrePaid {
		var err error
		var ok bool
		volumeChargePrepaidPeriodStr, ok := req.Parameters[DiskChargePrepaidPeriodAttr]
		if !ok {
			volumeChargePrepaidPeriodStr = strconv.Itoa(DiskChargePrepaidPeriodDefault)
		}

		volumeChargePrepaidPeriod, err = strconv.Atoi(volumeChargePrepaidPeriodStr)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "prepaid period not valid")
		}

		found := false

		for _, p := range DiskChargePrepaidPeriodValidValues {
			if p == volumeChargePrepaidPeriod {
				found = true
			}
		}

		if !found {
			return nil, status.Error(codes.InvalidArgument, "can not found valid prepaid period")
		}

		volumeChargePrepaidRenewFlag, ok = req.Parameters[DiskChargePrepaidRenewFlagAttr]
		if !ok {
			volumeChargePrepaidRenewFlag = DiskChargePrepaidRenewFlagDefault
		}
		if volumeChargePrepaidRenewFlag != DiskChargePrepaidRenewFlagDisableNotifyAndManualRenew && volumeChargePrepaidRenewFlag != DiskChargePrepaidRenewFlagNotifyAndAutoRenew && volumeChargePrepaidRenewFlag != DiskChargePrepaidRenewFlagNotifyAndManualRenewd {
			return nil, status.Error(codes.InvalidArgument, "invalid renew flag")
		}

	}

	volumeEncrypt, ok := req.Parameters[EncryptAttr]
	if !ok {
		volumeEncrypt = ""
	}

	if volumeEncrypt != "" && volumeEncrypt != EncryptEnable {
		return nil, status.Error(codes.InvalidArgument, "volume encrypt not valid")
	}

	createCbsReq := cbs.NewCreateDisksRequest()

	createCbsReq.ClientToken = &volumeIdempotencyName
	createCbsReq.DiskType = &volumeType
	createCbsReq.DiskChargeType = &volumeChargeType

	if volumeChargeType == DiskChargeTypePrePaid {
		period := uint64(volumeChargePrepaidPeriod)
		createCbsReq.DiskChargePrepaid = &cbs.DiskChargePrepaid{
			Period:    &period,
			RenewFlag: &volumeChargePrepaidRenewFlag,
		}
	}

	gb := uint64(volumeCapacity / int64(GB))

	createCbsReq.DiskSize = &gb

	if volumeEncrypt == EncryptEnable {
		createCbsReq.Encrypt = &EncryptEnable
	}

	createCbsReq.Placement = &cbs.Placement{
		Zone: &ctrl.zone,
	}

	createCbsResponse, err := ctrl.cbsClient.CreateDisks(createCbsReq)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if len(createCbsResponse.Response.DiskIdSet) <= 0 {
		return nil, status.Errorf(codes.Internal, "create disk failed, no disk id found in create disk response, request id %s", *createCbsResponse.Response.RequestId)
	}

	diskId := *createCbsResponse.Response.DiskIdSet[0]

	disk := new(cbs.Disk)

	ticker := time.NewTicker(time.Second * 5)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	for {
		select {
		case <-ticker.C:
			listCbsRequest := cbs.NewDescribeDisksRequest()
			listCbsRequest.DiskIds = []*string{&diskId}

			listCbsResponse, err := ctrl.cbsClient.DescribeDisks(listCbsRequest)
			if err != nil {
				continue
			}
			if len(listCbsResponse.Response.DiskSet) >= 1 {
				for _, d := range listCbsResponse.Response.DiskSet {
					if *d.DiskId == diskId && d.DiskState != nil {
						if *d.DiskState == StatusAttached || *d.DiskState == StatusUnattached {
							disk = d
							return &csi.CreateVolumeResponse{
								Volume: &csi.Volume{
									Id:            *disk.DiskId,
									CapacityBytes: int64(int(*disk.DiskSize) * GB),
								},
							}, nil
						}
					}
				}
			}
		case <-ctx.Done():
			return nil, status.Error(codes.DeadlineExceeded, "cbs disk is not ready before deadline exceeded")
		}
	}
}

func (ctrl *cbsController) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is empty")
	}

	describeDiskRequest := cbs.NewDescribeDisksRequest()
	describeDiskRequest.DiskIds = []*string{&req.VolumeId}
	describeDiskResponse, err := ctrl.cbsClient.DescribeDisks(describeDiskRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if len(describeDiskResponse.Response.DiskSet) <= 0 {
		return &csi.DeleteVolumeResponse{}, nil
	}

	terminateCbsRequest := cbs.NewTerminateDisksRequest()
	terminateCbsRequest.DiskIds = []*string{&req.VolumeId}

	_, err = ctrl.cbsClient.TerminateDisks(terminateCbsRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (ctrl *cbsController) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is empty")
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node id is empty")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume has no capabilities")
	}

	diskId := req.VolumeId
	instanceId := req.NodeId

	listCbsRequest := cbs.NewDescribeDisksRequest()
	listCbsRequest.DiskIds = []*string{&diskId}

	listCbsResponse, err := ctrl.cbsClient.DescribeDisks(listCbsRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if len(listCbsResponse.Response.DiskSet) <= 0 {
		return nil, status.Error(codes.NotFound, "disk not found")
	}

	for _, disk := range listCbsResponse.Response.DiskSet {
		if *disk.DiskId == diskId {
			if *disk.DiskState == StatusAttached && *disk.InstanceId == instanceId {
				return &csi.ControllerPublishVolumeResponse{}, nil
			}
			if *disk.DiskState == StatusAttached && *disk.InstanceId != instanceId {
				return nil, status.Error(codes.FailedPrecondition, "disk is attach to another instance already")
			}
		}
	}

	attachDiskRequest := cbs.NewAttachDisksRequest()
	attachDiskRequest.DiskIds = []*string{&diskId}
	attachDiskRequest.InstanceId = &instanceId

	_, err = ctrl.cbsClient.AttachDisks(attachDiskRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ticker := time.NewTicker(time.Second * 5)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	for {
		select {
		case <-ticker.C:
			listCbsRequest := cbs.NewDescribeDisksRequest()
			listCbsRequest.DiskIds = []*string{&diskId}

			listCbsResponse, err := ctrl.cbsClient.DescribeDisks(listCbsRequest)
			if err != nil {
				continue
			}
			if len(listCbsResponse.Response.DiskSet) >= 1 {
				for _, d := range listCbsResponse.Response.DiskSet {
					if *d.DiskId == diskId && d.DiskState != nil {
						if *d.DiskState == StatusAttached {
							return &csi.ControllerPublishVolumeResponse{}, nil
						}
					}
				}
			}
		case <-ctx.Done():
			return nil, status.Error(codes.Internal, "cbs disk is not attached before deadline exceeded")
		}
	}
}

func (ctrl *cbsController) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is empty")
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node id is empty")
	}

	diskId := req.VolumeId

	listCbsRequest := cbs.NewDescribeDisksRequest()
	listCbsRequest.DiskIds = []*string{&diskId}

	listCbsResponse, err := ctrl.cbsClient.DescribeDisks(listCbsRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if len(listCbsResponse.Response.DiskSet) <= 0 {
		return nil, status.Error(codes.NotFound, "disk not found")
	}

	for _, disk := range listCbsResponse.Response.DiskSet {
		if *disk.DiskId == diskId {
			if *disk.DiskState == StatusUnattached {
				return &csi.ControllerUnpublishVolumeResponse{}, nil
			}
		}
	}

	detachDiskRequest := cbs.NewDetachDisksRequest()
	detachDiskRequest.DiskIds = []*string{&diskId}

	_, err = ctrl.cbsClient.DetachDisks(detachDiskRequest)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ticker := time.NewTicker(time.Second * 5)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	for {
		select {
		case <-ticker.C:
			listCbsRequest := cbs.NewDescribeDisksRequest()
			listCbsRequest.DiskIds = []*string{&diskId}

			listCbsResponse, err := ctrl.cbsClient.DescribeDisks(listCbsRequest)
			if err != nil {
				continue
			}
			if len(listCbsResponse.Response.DiskSet) >= 1 {
				for _, d := range listCbsResponse.Response.DiskSet {
					if *d.DiskId == diskId && d.DiskState != nil {
						if *d.DiskState == StatusUnattached {
							return &csi.ControllerUnpublishVolumeResponse{}, nil
						}
					}
				}
			}
		case <-ctx.Done():
			return nil, status.Error(codes.Internal, "cbs disk is not unattached before deadline exceeded")
		}
	}
}

func (ctrl *cbsController) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (ctrl *cbsController) ValidateVolumeCapabilities(context.Context, *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ctrl *cbsController) ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ctrl *cbsController) GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ctrl *cbsController) CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ctrl *cbsController) DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ctrl *cbsController) ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
