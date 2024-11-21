// SPDX-License-Identifier: MIT
pragma solidity ^0.8.26;

contract Deposit {
  mapping (address => uint) public balances;

  function deposit() public payable {
    balances[msg.sender] += msg.value;
  }

  function verifyBalance(address account) public view returns (uint) {
    return balances[msg.sender];
  }
}