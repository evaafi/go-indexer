package indexer

import (
	"encoding/base64"
	"fmt"
	"math/big"

	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

const (
	MessageTypeLiquidation string = "liquidation"
	MessageTypeSupply      string = "supply"
	MessageTypeWithdraw    string = "withdraw"

	MessageSubTypeBorrow   string = "borrow"
	MessageSubTypeWithdraw string = "withdraw"
	MessageSubTypeSupply   string = "supply"
	MessageSubTypeRepay    string = "repay"

	LogOpCodeSupplySuccess    uint64 = 0x1
	LogOpCodeWithdrawSuccess  uint64 = 0x16
	LogOpCodeLiquidateSuccess uint64 = 0x3
)

func ParseLogMessage(boc string, logVersion int) (config.OnchainLog, error) {

	decoded, err := base64.StdEncoding.DecodeString(boc)
	if err != nil {
		return config.OnchainLog{}, fmt.Errorf("error base64 decode boc log: %w", err)
	}

	logCell, err := cell.FromBOC(decoded)
	// fmt.Printf("%d", logce)
	if err != nil {
		return config.OnchainLog{}, fmt.Errorf("error importing boc: %w", err)
	}

	slc := logCell.BeginParse()
	opCode, err := slc.LoadUInt(8)

	var idxLog config.OnchainLog
	switch opCode {
	case LogOpCodeSupplySuccess:
		idxLog = MustParseSupplyMessage(slc, logVersion)
		idxLog.TxType = MessageTypeSupply

		if idxLog.AttachedAssetPrincipal.Cmp(big.NewInt(0)) == 1 {
			idxLog.TxSubType = MessageSubTypeSupply
		} else {
			idxLog.TxSubType = MessageSubTypeRepay
		}
	case LogOpCodeWithdrawSuccess:
		idxLog = MustParseWithdrawMessage(slc, logVersion)
		idxLog.TxType = MessageTypeWithdraw

		if idxLog.RedeemedAssetPrincipal.Cmp(big.NewInt(0)) != -1 {
			idxLog.TxSubType = MessageSubTypeWithdraw
		} else {
			idxLog.TxSubType = MessageSubTypeBorrow
		}
	case LogOpCodeLiquidateSuccess:
		idxLog = MustParseLiquidateMessage(slc, logVersion)
		idxLog.TxType = MessageTypeLiquidation
	default:
		err = fmt.Errorf("unknown log type")
		idxLog.TxType = "unknown"
		return idxLog, err
	}

	bitsLeft := slc.BitsLeft()

	/*if idxLog.TxType == "withdraw" {
		fmt.Printf("%s BitsLeft > 0; %d %s %s\n", idxLog.TxType, bitsLeft, slc.String(), idxLog.RedeemedAssetAddress)
	}*/

	if bitsLeft > 0 {
		fmt.Printf("opcode %d", opCode)
		err = fmt.Errorf("error pasing tx: logversion: %d %s BitsLeft > 0; %d", logVersion, idxLog.TxType, bitsLeft)
	}

	return idxLog, err
}

func MustParseLiquidateMessage(slc *cell.Slice, logVersion int) config.OnchainLog {
	var idxLog config.OnchainLog

	idxLog.UserAddress = slc.MustLoadAddr().String()
	idxLog.SenderAddress = slc.MustLoadAddr().String()

	if logVersion == 1 {
		slc.MustLoadAddr()
	}

	idxLog.Utime = int64(slc.MustLoadUInt(32))

	if slc.BitsLeft() == 16 { // todo rm all bitsleft checks, replace with check logVersion after release
		idxLog.SubaccountID = int16(slc.MustLoadInt(16))
	} else {
		idxLog.SubaccountID = 0
	}

	loanAssetData := slc.MustLoadRef()
	idxLog.AttachedAssetAddress = config.BigInt{Int: loanAssetData.MustLoadBigUInt(256)}
	idxLog.AttachedAssetAmount = config.BigInt{Int: loanAssetData.MustLoadBigUInt(64)}
	idxLog.AttachedAssetPrincipal = config.BigInt{Int: big.NewInt(loanAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetTotalSupplyPrincipal = config.BigInt{Int: big.NewInt(loanAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetTotalBorrowPrincipal = config.BigInt{Int: big.NewInt(loanAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetSRate = config.BigInt{Int: loanAssetData.MustLoadBigUInt(64)}
	idxLog.AttachedAssetBRate = config.BigInt{Int: loanAssetData.MustLoadBigUInt(64)}

	collateralAssetData := slc.MustLoadRef()
	idxLog.RedeemedAssetAddress = config.BigInt{Int: collateralAssetData.MustLoadBigUInt(256)}
	idxLog.RedeemedAssetAmount = config.BigInt{Int: collateralAssetData.MustLoadBigUInt(64)}
	idxLog.RedeemedAssetPrincipal = config.BigInt{Int: big.NewInt(collateralAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetTotalSupplyPrincipal = config.BigInt{Int: big.NewInt(collateralAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetTotalBorrowPrincipal = config.BigInt{Int: big.NewInt(collateralAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetSRate = config.BigInt{Int: collateralAssetData.MustLoadBigUInt(64)}
	idxLog.RedeemedAssetBRate = config.BigInt{Int: collateralAssetData.MustLoadBigUInt(64)}

	return idxLog
}

func MustParseWithdrawMessage(slc *cell.Slice, logVersion int) config.OnchainLog {
	var idxLog config.OnchainLog

	idxLog.UserAddress = slc.MustLoadAddr().String()
	idxLog.SenderAddress = slc.MustLoadAddr().String()

	if logVersion == 1 {
		slc.MustLoadAddr()
	}

	idxLog.Utime = int64(slc.MustLoadUInt(32))
	if slc.BitsLeft() == 16 {  // new version of withdraw log
		idxLog.SubaccountID = int16(slc.MustLoadInt(16))

		attachedAssetData := slc.MustLoadRef()
		idxLog.AttachedAssetAddress = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(256)}
		idxLog.AttachedAssetAmount = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}
		idxLog.AttachedAssetPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
		idxLog.AttachedAssetTotalSupplyPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
		idxLog.AttachedAssetTotalBorrowPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
		idxLog.AttachedAssetSRate = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}
		idxLog.AttachedAssetBRate = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}
	} else {
		idxLog.SubaccountID = 0
		slc.MustLoadRef()
	}

	redeemedAssetData := slc.MustLoadRef()
	idxLog.RedeemedAssetAddress = config.BigInt{Int: redeemedAssetData.MustLoadBigUInt(256)}
	idxLog.RedeemedAssetAmount = config.BigInt{Int: redeemedAssetData.MustLoadBigUInt(64)}
	idxLog.RedeemedAssetPrincipal = config.BigInt{Int: big.NewInt(redeemedAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetTotalSupplyPrincipal = config.BigInt{Int: big.NewInt(redeemedAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetTotalBorrowPrincipal = config.BigInt{Int: big.NewInt(redeemedAssetData.MustLoadInt(64))}
	idxLog.RedeemedAssetSRate = config.BigInt{Int: redeemedAssetData.MustLoadBigUInt(64)}
	idxLog.RedeemedAssetBRate = config.BigInt{Int: redeemedAssetData.MustLoadBigUInt(64)}

	return idxLog
}

func MustParseSupplyMessage(slc *cell.Slice, logVersion int) config.OnchainLog {
	var idxLog config.OnchainLog

	idxLog.UserAddress = slc.MustLoadAddr().String()
	idxLog.SenderAddress = slc.MustLoadAddr().String()
	idxLog.Utime = int64(slc.MustLoadUInt(32))

	if slc.BitsLeft() == 16 {
		idxLog.SubaccountID = int16(slc.MustLoadInt(16))
	} else {
		idxLog.SubaccountID = 0
	}
	attachedAssetData := slc.MustLoadRef()
	idxLog.AttachedAssetAddress = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(256)}
	idxLog.AttachedAssetAmount = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}
	idxLog.AttachedAssetPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetTotalSupplyPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetTotalBorrowPrincipal = config.BigInt{Int: big.NewInt(attachedAssetData.MustLoadInt(64))}
	idxLog.AttachedAssetSRate = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}
	idxLog.AttachedAssetBRate = config.BigInt{Int: attachedAssetData.MustLoadBigUInt(64)}

	return idxLog
}
