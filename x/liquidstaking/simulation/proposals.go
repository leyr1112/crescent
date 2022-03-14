package simulation

import (
	"encoding/json"
	"fmt"
	"math/rand"

	sdk "github.com/cosmos/cosmos-sdk/types"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/params/types/proposal"
	"github.com/cosmos/cosmos-sdk/x/simulation"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/cosmosquad-labs/squad/app/params"
	"github.com/cosmosquad-labs/squad/x/liquidstaking/keeper"
	"github.com/cosmosquad-labs/squad/x/liquidstaking/types"
)

// Simulation operation weights constants.
const (
	OpWeightSimulateAddWhitelistValidatorsProposal    = "op_weight_add_whitelist_validators_proposal"
	OpWeightSimulateUpdateWhitelistValidatorsProposal = "op_weight_update_whitelist_validators_proposal"
	OpWeightSimulateDeleteWhitelistValidatorsProposal = "op_weight_delete_whitelist_validators_proposal"
	OpWeightCompleteRedelegationUnbonding             = "op_weight_complete_redelegation_unbonding"
	OpWeightTallyWithLiquidStaking                    = "op_weight_tally_with_liquid_staking"
	MaxWhitelistValidators                            = 10
)

// ProposalContents defines the module weighted proposals' contents for mocking param changes, other actions with keeper
func ProposalContents(ak types.AccountKeeper, bk types.BankKeeper, sk types.StakingKeeper, gk types.GovKeeper, k keeper.Keeper) []simtypes.WeightedProposalContent {
	return []simtypes.WeightedProposalContent{
		simulation.NewWeightedProposalContent(
			OpWeightSimulateAddWhitelistValidatorsProposal,
			params.DefaultWeightAddWhitelistValidatorsProposal,
			SimulateAddWhitelistValidatorsProposal(sk, k),
		),
		simulation.NewWeightedProposalContent(
			OpWeightSimulateUpdateWhitelistValidatorsProposal,
			params.DefaultWeightUpdateWhitelistValidatorsProposal,
			SimulateUpdateWhitelistValidatorsProposal(sk, k),
		),
		simulation.NewWeightedProposalContent(
			OpWeightSimulateDeleteWhitelistValidatorsProposal,
			params.DefaultWeightDeleteWhitelistValidatorsProposal,
			SimulateDeleteWhitelistValidatorsProposal(sk, k),
		),
		simulation.NewWeightedProposalContent(
			OpWeightCompleteRedelegationUnbonding,
			params.DefaultWeightCompleteRedelegationUnbonding,
			SimulateCompleteRedelegationUnbonding(sk, k),
		),
		simulation.NewWeightedProposalContent(
			OpWeightTallyWithLiquidStaking,
			params.DefaultWeightTallyWithLiquidStaking,
			SimulateTallyWithLiquidStaking(ak, bk, gk, k),
		),
	}
}

// SimulateAddWhitelistValidatorsProposal generates random add whitelisted validator param change proposal content.
func SimulateAddWhitelistValidatorsProposal(sk types.StakingKeeper, k keeper.Keeper) simtypes.ContentSimulatorFn {
	return func(r *rand.Rand, ctx sdk.Context, accs []simtypes.Account) simtypes.Content {
		params := k.GetParams(ctx)

		vals := sk.GetBondedValidatorsByPower(ctx)

		wm := params.WhitelistedValMap()
		for i := 0; i < len(vals) && len(params.WhitelistedValidators) < MaxWhitelistValidators; i++ {
			val, _ := keeper.RandomValidator(r, sk, ctx)
			if _, ok := wm[val.OperatorAddress]; !ok {
				params.WhitelistedValidators = append(params.WhitelistedValidators,
					types.WhitelistedValidator{
						ValidatorAddress: val.OperatorAddress,
						TargetWeight:     genTargetWeight(r),
					})
				fmt.Println("## added vals", val.OperatorAddress)
				break
			}
		}

		whitelistStr, err := json.Marshal(&params.WhitelistedValidators)
		if err != nil {
			panic(err)
		}
		change := proposal.NewParamChange(types.ModuleName, string(types.KeyWhitelistedValidators), string(whitelistStr))

		// manually set params for simulation
		k.SetParams(ctx, params)

		// this proposal could be passed due to x/gov simulation voting process
		return proposal.NewParameterChangeProposal(
			"AddWhitelistValidatorsProposal",
			"AddWhitelistValidatorsProposal",
			[]proposal.ParamChange{change},
		)
	}
}

// SimulateUpdateWhitelistValidatorsProposal generates random update whitelisted validator param change proposal content.
func SimulateUpdateWhitelistValidatorsProposal(sk types.StakingKeeper, k keeper.Keeper) simtypes.ContentSimulatorFn {
	return func(r *rand.Rand, ctx sdk.Context, accs []simtypes.Account) simtypes.Content {
		params := k.GetParams(ctx)

		targetVal, found := keeper.RandomActiveLiquidValidator(r, ctx, k, sk)
		if found {
			for i := range params.WhitelistedValidators {
				if params.WhitelistedValidators[i].ValidatorAddress == targetVal.OperatorAddress {
					params.WhitelistedValidators[i].TargetWeight = genTargetWeight(r)
					fmt.Println("## update vals", targetVal.OperatorAddress)
					k.SetParams(ctx, params)
					break
				}
			}
		}

		whitelistStr, err := json.Marshal(&params.WhitelistedValidators)
		if err != nil {
			panic(err)
		}
		change := proposal.NewParamChange(types.ModuleName, string(types.KeyWhitelistedValidators), string(whitelistStr))

		// manually set params for simulation
		k.SetParams(ctx, params)

		// this proposal could be passed due to x/gov simulation voting process
		return proposal.NewParameterChangeProposal(
			"UpdateWhitelistValidatorsProposal",
			"UpdateWhitelistValidatorsProposal",
			[]proposal.ParamChange{change},
		)
	}
}

// SimulateDeleteWhitelistValidatorsProposal generates random delete whitelisted validator param change proposal content.
func SimulateDeleteWhitelistValidatorsProposal(sk types.StakingKeeper, k keeper.Keeper) simtypes.ContentSimulatorFn {
	return func(r *rand.Rand, ctx sdk.Context, accs []simtypes.Account) simtypes.Content {
		params := k.GetParams(ctx)

		targetVal, found := keeper.RandomActiveLiquidValidator(r, ctx, k, sk)
		if found {
			remove := func(slice []types.WhitelistedValidator, s int) []types.WhitelistedValidator {
				return append(slice[:s], slice[s+1:]...)
			}

			for i := range params.WhitelistedValidators {
				if params.WhitelistedValidators[i].ValidatorAddress == targetVal.OperatorAddress {
					params.WhitelistedValidators[i].TargetWeight = genTargetWeight(r)
					params.WhitelistedValidators = remove(params.WhitelistedValidators, i)
					fmt.Println("## delete vals", targetVal.OperatorAddress)
					k.SetParams(ctx, params)
					break
				}
			}
		}

		whitelistStr, err := json.Marshal(&params.WhitelistedValidators)
		if err != nil {
			panic(err)
		}
		change := proposal.NewParamChange(types.ModuleName, string(types.KeyWhitelistedValidators), string(whitelistStr))

		// this proposal could be passed due to x/gov simulation voting process
		return proposal.NewParameterChangeProposal(
			"SimulateDeleteWhitelistValidatorsProposal",
			"SimulateDeleteWhitelistValidatorsProposal",
			[]proposal.ParamChange{change},
		)
	}
}

// SimulateCompleteRedelegationUnbonding mocking complete redelegations, unbondings by BlockValidatorUpdates of staking keeper.
func SimulateCompleteRedelegationUnbonding(sk types.StakingKeeper, k keeper.Keeper) simtypes.ContentSimulatorFn {
	return func(r *rand.Rand, ctx sdk.Context, accs []simtypes.Account) simtypes.Content {
		reds := sk.GetAllRedelegations(ctx, types.LiquidStakingProxyAcc, nil, nil)
		ubds := sk.GetAllUnbondingDelegations(ctx, types.LiquidStakingProxyAcc)
		if len(reds) != 0 || len(ubds) != 0 {
			fmt.Println("## SimulateCompleteRedelegationUnbonding", ctx.BlockTime())
			ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 100).WithBlockTime(ctx.BlockTime().Add(stakingtypes.DefaultUnbondingTime))
			sk.BlockValidatorUpdates(ctx)
		}
		params := k.GetParams(ctx)

		whitelistStr, err := json.Marshal(&params.WhitelistedValidators)
		if err != nil {
			panic(err)
		}
		change := proposal.NewParamChange(types.ModuleName, string(types.KeyWhitelistedValidators), string(whitelistStr))

		// this proposal could be passed due to x/gov simulation voting process
		return proposal.NewParameterChangeProposal(
			"SimulateCompleteRedelegationUnbonding",
			"SimulateCompleteRedelegationUnbonding",
			[]proposal.ParamChange{change},
		)
	}
}

// SimulateTallyWithLiquidStaking mocking tally for SetLiquidStakingVotingPowers.
func SimulateTallyWithLiquidStaking(ak types.AccountKeeper, bk types.BankKeeper, gk types.GovKeeper, k keeper.Keeper) simtypes.ContentSimulatorFn {
	return func(r *rand.Rand, ctx sdk.Context, accs []simtypes.Account) simtypes.Content {
		proposals := gk.GetProposals(ctx)
		var targetProposal *govtypes.Proposal
		for _, p := range proposals {
			if p.Status == govtypes.StatusVotingPeriod {
				targetProposal = &p
				break
			}
		}
		var voter sdk.AccAddress
		if targetProposal != nil {
			for i := 1; i < len(accs); i++ {
				simAccount, _ := simtypes.RandomAcc(r, accs)

				account := ak.GetAccount(ctx, simAccount.Address)
				spendable := bk.SpendableCoins(ctx, account.GetAddress())

				// spendable must be greater than unstaking coins
				if spendable.AmountOf(types.DefaultLiquidBondDenom).GT(sdk.ZeroInt()) {
					voter = account.GetAddress()
					err := gk.AddVote(ctx, targetProposal.ProposalId, voter, govtypes.WeightedVoteOptions{
						govtypes.WeightedVoteOption{Option: govtypes.OptionYes, Weight: sdk.NewDec(1)},
					})
					if err != nil {
						panic(err)
					}
					_, _, res := gk.Tally(ctx, *targetProposal)
					fmt.Println("## SimulateTallyWithLiquidStaking", res)
					break
				}
			}
		}

		params := k.GetParams(ctx)

		whitelistStr, err := json.Marshal(&params.WhitelistedValidators)
		if err != nil {
			panic(err)
		}
		change := proposal.NewParamChange(types.ModuleName, string(types.KeyWhitelistedValidators), string(whitelistStr))

		// this proposal could be passed due to x/gov simulation voting process
		return proposal.NewParameterChangeProposal(
			"SimulateTallyWithLiquidStaking",
			"SimulateTallyWithLiquidStaking",
			[]proposal.ParamChange{change},
		)
	}
}
