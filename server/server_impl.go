package mvcamctrl

import (
	"context"
	"encoding/binary"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/op/go-logging"
	"github.com/wuyuanyi135/mvcamctrl/serial"
	"github.com/wuyuanyi135/mvcamctrl/serial/command"
	"github.com/wuyuanyi135/mvprotos/mvcamctrl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

const DriverVersion = "1.0"

var log = logging.MustGetLogger("Server")

type LaserCtrlServer struct {
	serialInstance   serial.Serial
	openedSerialPort string
}

func New() *LaserCtrlServer {
	return &LaserCtrlServer{serialInstance: serial.NewSerial()}
}

func (s *LaserCtrlServer) GetSerialDevices(context.Context, *empty.Empty) (*mvcamctrl.SerialListResponse, error) {
	resp := mvcamctrl.SerialListResponse{}

	devList, err := serial.ListSerialPorts()
	if err != nil {
		return &resp, err
	}

	for name, destination := range devList {
		opened := false
		if s.openedSerialPort != "" && s.openedSerialPort == destination {
			opened = true
		}
		resp.DeviceList = append(resp.DeviceList, &mvcamctrl.SerialDeviceMapping{
			Name:        name,
			Destination: destination,
			Connected:   opened,
		})
	}

	return &resp, nil
}

func (LaserCtrlServer) GetDriverVersion(context.Context, *empty.Empty) (*mvcamctrl.DriverVersionResponse, error) {
	resp := mvcamctrl.DriverVersionResponse{
		Version: DriverVersion,
	}

	return &resp, nil
}

func (s *LaserCtrlServer) Connect(ctx context.Context, request *mvcamctrl.ConnectRequest) (*mvcamctrl.EmptyResponse, error) {
	var err error
	switch request.DeviceIdentifier.(type) {
	case *mvcamctrl.ConnectRequest_Path:
		err = s.serialInstance.ConnectByPath(request.GetPath())
	case *mvcamctrl.ConnectRequest_Name:
		err = s.serialInstance.ConnectByName(request.GetName())
	}

	var resp mvcamctrl.EmptyResponse

	return &resp, err
}

func (s *LaserCtrlServer) Disconnect(context.Context, *empty.Empty) (*mvcamctrl.EmptyResponse, error) {
	err := s.serialInstance.Disconnect()

	var resp mvcamctrl.EmptyResponse
	return &resp, err
}

func (s *LaserCtrlServer) deviceRequest(ctx context.Context, command serial.SerialCommand) ([]byte, error) {
	if ctx != nil {
		command.Ctx = ctx
	}

	if command.ResponseChannel == nil {
		command.ResponseChannel = make(chan []byte)
	}

	err := s.serialInstance.WriteCommandAndRegisterResponse(command)
	if err != nil {
		return nil, err
	}

	select {
	case response := <-command.ResponseChannel:
		return response, nil
	case <-ctx.Done():
		return nil, status.Errorf(codes.DeadlineExceeded, "%#v command time out", command)
	}
}

func (s *LaserCtrlServer) GetDeviceVersion(ctx context.Context, req *empty.Empty) (*mvcamctrl.DeviceVersionResponse, error) {
	var resp = mvcamctrl.DeviceVersionResponse{}
	ctx, _ = context.WithTimeout(ctx, time.Second)
	version, err := s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandVersion,
			Arg:     nil,
		},
	)
	if err != nil {
		log.Errorf("Get device version error: %s", err.Error())
		return nil, err
	}

	resp.HardwareVersion = uint32(version[0])
	resp.FirmwareVersion = uint32(version[1])

	return &resp, nil
}

func (s *LaserCtrlServer) SetPower(ctx context.Context, req *mvcamctrl.SetPowerRequest) (*mvcamctrl.EmptyResponse, error) {
	resp := mvcamctrl.EmptyResponse{}

	ctx, _ = context.WithTimeout(ctx, time.Second)
	var power byte = 0
	if req.Power.MasterPower {
		power = 1
	}
	_, err := s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandSetPower,
			Arg:     []byte{power},
		})
	if err != nil {
		log.Errorf("Set power error: %s", err.Error())
		return nil, err
	}

	return &resp, nil
}

func (s *LaserCtrlServer) GetPower(ctx context.Context, req *empty.Empty) (*mvcamctrl.PowerConfiguration, error) {
	resp := mvcamctrl.PowerConfiguration{}

	ctx, _ = context.WithTimeout(ctx, time.Second)
	power, err := s.deviceRequest(ctx, serial.SerialCommand{
		Command: command.CommandGetPower,
	})
	if err != nil {
		log.Errorf("Get power error: %s", err.Error())
		return nil, err
	}

	resp.MasterPower = power[0] == 1
	return &resp, nil
}

func (s *LaserCtrlServer) SetLaserParam(ctx context.Context, req *mvcamctrl.SetLaserRequest) (*mvcamctrl.EmptyResponse, error) {
	resp := mvcamctrl.EmptyResponse{}
	ctx, _ = context.WithTimeout(ctx, time.Second)

	config := req.Laser

	b := make([]byte, command.CommandSetExposure.RequestLength)
	binary.LittleEndian.PutUint16(b, uint16(config.ExposureTick))
	_, err := s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandSetExposure,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to set exposure: %s", err)
		return nil, err
	}

	b = make([]byte, command.CommandSetFilter.RequestLength)
	binary.LittleEndian.PutUint16(b, uint16(config.DigitalFilter))
	_, err = s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandSetFilter,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to set filter: %s", err)
		return nil, err
	}

	b = make([]byte, command.CommandSetDelay.RequestLength)
	binary.LittleEndian.PutUint16(b, uint16(config.PulseDelay))
	_, err = s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandSetDelay,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to set exposure: %s", err)
		return nil, err
	}

	return &resp, nil
}

func (s *LaserCtrlServer) GetLaserParam(ctx context.Context, req *empty.Empty) (*mvcamctrl.LaserConfiguration, error) {
	resp := mvcamctrl.LaserConfiguration{}
	ctx, _ = context.WithTimeout(ctx, time.Second)
	// exposure
	b := make([]byte, command.CommandGetExposure.ResponseLength)

	param, err := s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandGetExposure,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to get exposure: %s", err)
		return nil, err
	}

	resp.ExposureTick = uint32(binary.LittleEndian.Uint16(param))

	// filter
	b = make([]byte, command.CommandGetFilter.ResponseLength)

	param, err = s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandGetFilter,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to get filter: %s", err)
		return nil, err
	}

	resp.DigitalFilter = uint32(binary.LittleEndian.Uint16(param))

	// delay
	b = make([]byte, command.CommandGetDelay.ResponseLength)

	param, err = s.deviceRequest(
		ctx,
		serial.SerialCommand{
			Command: command.CommandGetDelay,
			Arg:     b,
		},
	)
	if err != nil {
		log.Errorf("Failed to get delay: %s", err)
		return nil, err
	}

	resp.PulseDelay = uint32(binary.LittleEndian.Uint16(param))

	return &resp, nil
}

func (s *LaserCtrlServer) CommitParameter(ctx context.Context, req *empty.Empty) (*mvcamctrl.EmptyResponse, error) {
	resp := mvcamctrl.EmptyResponse{}
	ctx, _ = context.WithTimeout(ctx, time.Second)

	_, err := s.deviceRequest(ctx, serial.SerialCommand{
		Command: command.CommandCommitParameters,
	})

	if err != nil {
		log.Errorf("Failed to commit parameter: %s", err.Error())
		return nil, err
	}
	return &resp, err
}

func (s *LaserCtrlServer) ControlLaser(ctx context.Context, req *mvcamctrl.ControlLaserRequest) (*mvcamctrl.EmptyResponse, error) {
	resp := mvcamctrl.EmptyResponse{}
	ctx, _ = context.WithTimeout(ctx, time.Second)

	var cmd command.CommandMeta
	if req.Enable {
		cmd = command.CommandArmTrigger
	} else {
		cmd = command.CommandCancelTrigger
	}

	_, err := s.deviceRequest(ctx, serial.SerialCommand{
		Command: cmd,
	})
	if err != nil {
		log.Errorf("Failed to control laser: %s", err.Error())
		return nil, err
	}
	return &resp, err
}

func (s *LaserCtrlServer) ResetController(ctx context.Context, req *empty.Empty) (*mvcamctrl.EmptyResponse, error) {
	resp := mvcamctrl.EmptyResponse{}
	ctx, _ = context.WithTimeout(ctx, time.Second)

	_, err := s.deviceRequest(ctx, serial.SerialCommand{
		Command: command.CommandReset,
	})

	if err != nil {
		log.Errorf("Failed to reset: %s", err.Error())
		return nil, err
	}
	return &resp, err
}
