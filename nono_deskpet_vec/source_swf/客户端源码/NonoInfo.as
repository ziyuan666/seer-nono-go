package com.robot.core.info
{
   import flash.utils.IDataInput;
   import org.taomee.utils.BitUtil;
   
   public class NonoInfo
   {
      
      public var userID:uint;
      
      public var flag:Array = [];
      
      public var state:Array = [];
      
      public var nick:String;
      
      public var superNono:Boolean;
      
      public var isExtremeNono:Boolean;
      
      public var color:uint;
      
      public var power:Number;
      
      public var mate:Number;
      
      public var iq:uint;
      
      public var ai:uint;
      
      public var birth:Number;
      
      public var chargeTime:uint;
      
      public var func:Array = [];
      
      public var superEnergy:Number;
      
      public var superLevel:uint;
      
      public var superStage:uint;
      
      public var flyStyle:uint;
      
      public function NonoInfo(param1:IDataInput = null)
      {
         super();
         if(Boolean(param1))
         {
            this.userID = param1.readUnsignedInt();
            var _loc2_:uint = uint(param1.readUnsignedInt());
            var _loc3_:uint = uint(param1.readUnsignedInt());
            var _loc4_:int = 0;
            this.flag = [];
            this.state = [];
            while(_loc4_ < 32)
            {
               this.flag.push(Boolean(BitUtil.getBit(_loc2_,_loc4_)));
               this.state.push(Boolean(BitUtil.getBit(_loc3_,_loc4_)));
               _loc4_++;
            }
            this.nick = param1.readUTFBytes(16);
            var _loc7_:uint = uint(param1.readUnsignedInt());
            this.superNono = _loc7_ > 0;
            this.isExtremeNono = _loc7_ >= 6;
            this.color = param1.readUnsignedInt();
            this.power = Number(param1.readUnsignedInt()) / 1000;
            this.mate = Number(param1.readUnsignedInt()) / 1000;
            this.iq = param1.readUnsignedInt();
            this.ai = uint(param1.readUnsignedShort());
            this.birth = Number(param1.readUnsignedInt());
            this.chargeTime = param1.readUnsignedInt();
            this.func = [];
            var _loc8_:int = 0;
            while(_loc8_ < 20)
            {
               var _loc5_:uint = uint(param1.readUnsignedByte());
               var _loc6_:int = 0;
               while(_loc6_ < 8)
               {
                  this.func.push(Boolean(BitUtil.getBit(_loc5_,_loc6_)));
                  _loc6_++;
               }
               _loc8_++;
            }
            this.superEnergy = Number(param1.readUnsignedInt());
            this.superLevel = param1.readUnsignedInt();
            this.superStage = param1.readUnsignedInt();
         }
      }
      
      public function getMateLevel() : uint
      {
         if(this.mate <= 30)
         {
            return 1;
         }
         if(this.mate >= 31 && this.mate <= 69)
         {
            return 2;
         }
         return 3;
      }
      
      public function getPowerLevel() : uint
      {
         if(this.power <= 30)
         {
            return 1;
         }
         if(this.power >= 31 && this.power <= 69)
         {
            return 2;
         }
         return 3;
      }
   }
}

